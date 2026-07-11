package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Editors typically fire several events per save (write + rename + chmod);
// changes are collected until the burst quiets down and then handled as one
// batch
const debounceDelay = 300 * time.Millisecond

// Only paths inside a project source tree are handled; everything else that
// can reach the watcher (siblings of watched symlink targets, the watched
// roots themselves) is skipped
var sourceTreeRegex = regexp.MustCompile(`/src/main/(mule|resources)/`)

// isUnder reports whether path is dir itself or lies below it
func isUnder(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
}

func watchDirs(projectsDir string) []string {
	// Track current dir and one level down
	pd := removeTrailingSlash(projectsDir)
	var dirs []string
	for _, pattern := range []string{
		pd + "/*/src/main/mule",
		pd + "/src/main/mule",
		pd + "/*/src/main/resources",
		pd + "/src/main/resources",
	} {
		for _, m := range globVisible(pattern, pd) {
			if dirExists(m) && !ignoreFile(m+"/") {
				dirs = append(dirs, m)
			}
		}
	}
	if opts.Verbose {
		fmt.Printf("Watching directories : %v\n", dirs)
	}
	return dirs
}

// globVisible is filepath.Glob minus matches with a hidden path component
// below base, mirroring shell glob behavior where '*' does not match a
// leading dot (keeps sibling checkouts like .claude/worktrees out of the
// watch set)
func globVisible(pattern, base string) []string {
	matches, _ := filepath.Glob(pattern)
	var out []string
	for _, m := range matches {
		m = filepath.ToSlash(m)
		rel := strings.TrimPrefix(m, ensureTrailingSlash(base))
		hidden := false
		for _, part := range strings.Split(rel, "/") {
			if strings.HasPrefix(part, ".") {
				hidden = true
				break
			}
		}
		if !hidden {
			out = append(out, m)
		}
	}
	return out
}

type sourceWatcher struct {
	fsw   *fsnotify.Watcher
	roots []string
	deb   *debouncer
	mu    sync.Mutex
	// set once this instance has been replaced by a restart: batch
	// processing and recovery goroutines must stand down
	stopped bool
	// files already seen, by logical path: distinguishes "added" from
	// "modified", since editors often save via create+rename
	known map[string]bool
	// real directories (including symlink-target parents) with an active watch
	watchedDirs map[string]bool
	// real symlink-target path → logical path under the project tree;
	// populated only with -s/--follow-symlinks
	linkMap map[string]string
	// watched roots currently missing from disk, being polled for recreation
	missingRoots map[string]bool
	// symlink targets currently missing from disk, being polled for recreation
	recovering map[string]bool
}

func watchMuleAndResources() {
	if !dirExists(opts.AppsDir) {
		fmt.Printf("Directory %s does not exist\n", opts.AppsDir)
		fmt.Println("Make sure that MULE_HOME is set and points to an existing mule-server directory, or specify the --apps-dir script option")
		os.Exit(1)
	}
	if !dirExists(opts.ProjectsDir) {
		fmt.Printf("Directory %s does not exist\n", opts.ProjectsDir)
		fmt.Println("Make sure that --projects-dir points to an existing directory")
		os.Exit(1)
	}
	if err := startSourceWatcher(nil); err != nil {
		fmt.Printf("ERROR: could not start file watcher: %v\n", err)
		os.Exit(1)
	}
}

// startSourceWatcher builds a watcher over the project source trees.
// prevRoots carries the previous instance's roots across a restart, so a
// root that happens to be missing from disk at restart time is polled for
// recreation instead of forgotten.
func startSourceWatcher(prevRoots []string) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	roots := watchDirs(opts.ProjectsDir)
	for _, prev := range prevRoots {
		found := false
		for _, r := range roots {
			if r == prev {
				found = true
				break
			}
		}
		if !found {
			roots = append(roots, prev)
		}
	}
	w := &sourceWatcher{
		fsw:          fsw,
		roots:        roots,
		known:        map[string]bool{},
		watchedDirs:  map[string]bool{},
		linkMap:      map[string]string{},
		missingRoots: map[string]bool{},
		recovering:   map[string]bool{},
	}
	w.deb = newDebouncer(debounceDelay, w.processBatch)
	w.mu.Lock()
	for _, dir := range w.roots {
		if dirExists(dir) {
			w.addTree(dir, dir)
		} else if !w.missingRoots[dir] {
			w.missingRoots[dir] = true
			go w.recoverRoot(dir)
		}
	}
	w.mu.Unlock()
	go w.eventLoop()
	return nil
}

// addTree watches realDir and everything below it, returning the files it
// discovered (by logical path) that were not known before. logicalDir is the
// path as seen from the project tree; it differs from realDir below external
// symlinks, whose resolved targets are watched directly so changes behind
// them produce native events. Must be called with w.mu held.
func (w *sourceWatcher) addTree(realDir, logicalDir string) []string {
	if err := w.fsw.Add(realDir); err != nil {
		fmt.Printf("WARNING: could not watch %s: %v\n", realDir, err)
		return nil
	}
	w.watchedDirs[realDir] = true
	if realDir != logicalDir {
		w.linkMap[realDir] = logicalDir
	}
	entries, err := os.ReadDir(realDir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		realPath := realDir + "/" + entry.Name()
		logicalPath := logicalDir + "/" + entry.Name()
		if entry.Type()&fs.ModeSymlink != 0 {
			found, _ := w.addSymlink(realPath, logicalPath)
			files = append(files, found...)
		} else if entry.IsDir() {
			files = append(files, w.addTree(realPath, logicalPath)...)
		} else if !w.known[logicalPath] {
			w.known[logicalPath] = true
			files = append(files, logicalPath)
		}
	}
	return files
}

// addSymlink handles a symlink found in the watched tree. Only symlinks
// pointing outside every watched root are followed (with
// -s/--follow-symlinks): targets inside the tree are already watched under
// their real path. Directory targets are watched as a subtree mapped back to
// the symlink's logical path. For file targets the target's PARENT directory
// is watched — a watch on the file itself would be orphaned the first time
// an editor replaces it via write-temp-then-rename — and only the exact
// target path is mapped. Returns the discovered files and whether the
// symlink was followed. Must be called with w.mu held.
func (w *sourceWatcher) addSymlink(realPath, logicalPath string) ([]string, bool) {
	if !opts.FollowSymlinks {
		return nil, false
	}
	target, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		return nil, false
	}
	target = filepath.ToSlash(target)
	if w.insideRoots(target) {
		return nil, false
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, false
	}
	if info.IsDir() {
		if opts.Verbose {
			fmt.Printf("Following symlinked directory: %s -> %s\n", logicalPath, target)
		}
		return w.addTree(target, logicalPath), true
	}
	parent := filepath.ToSlash(filepath.Dir(target))
	if !w.watchedDirs[parent] {
		if err := w.fsw.Add(parent); err != nil {
			fmt.Printf("WARNING: could not watch %s: %v\n", parent, err)
			return nil, false
		}
		w.watchedDirs[parent] = true
	}
	w.linkMap[target] = logicalPath
	if w.known[logicalPath] {
		return nil, true
	}
	w.known[logicalPath] = true
	return []string{logicalPath}, true
}

// insideRoots reports whether path lies under any watched root
func (w *sourceWatcher) insideRoots(path string) bool {
	for _, root := range w.roots {
		if isUnder(path, root) {
			return true
		}
	}
	return false
}

// toLogical maps a real event path back into the project tree when it came
// from a watched symlink target. The second return value is the logical path
// of the symlink the mapping goes through ("" when unmapped). Must be called
// with w.mu held.
func (w *sourceWatcher) toLogical(realPath string) (string, string) {
	if len(w.linkMap) == 0 {
		return realPath, ""
	}
	best, bestLogical := "", ""
	for real, logical := range w.linkMap {
		if isUnder(realPath, real) && len(real) > len(best) {
			best, bestLogical = real, logical
		}
	}
	if best == "" {
		return realPath, ""
	}
	return bestLogical + strings.TrimPrefix(realPath, best), bestLogical
}

func (w *sourceWatcher) eventLoop() {
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				w.restart()
				return
			}
			if ev.Op == fsnotify.Chmod || ev.Name == "" {
				continue
			}
			w.deb.add(filepath.ToSlash(ev.Name))
		case err, ok := <-w.fsw.Errors:
			if !ok {
				w.restart()
				return
			}
			fmt.Printf("ERROR: file watcher: %v\n", err)
		}
	}
}

// restart rebuilds the watcher from scratch if its event stream dies, so a
// backend failure degrades to a re-scan instead of silently stopping syncs.
// The old instance is fully stood down first: its debouncer stops firing and
// its recovery goroutines see the stopped flag, so old and new instances can
// never process the same app concurrently.
func (w *sourceWatcher) restart() {
	fmt.Println("ERROR: file watcher stopped, restarting...")
	w.mu.Lock()
	w.stopped = true
	roots := append([]string(nil), w.roots...)
	w.mu.Unlock()
	w.deb.stop()
	w.fsw.Close()
	for {
		if err := startSourceWatcher(roots); err == nil {
			return
		}
		time.Sleep(5 * time.Second)
	}
}

func (w *sourceWatcher) processBatch(paths []string) {
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if stopped {
		return
	}
	if opts.Verbose {
		fmt.Println("Changes detected")
	}
	for _, realPath := range paths {
		w.mu.Lock()
		logical, viaSymlink := w.toLogical(realPath)
		w.mu.Unlock()
		if opts.Verbose && logical != realPath {
			fmt.Printf("Processing event: %s (logical: %s)\n", realPath, logical)
		}
		// Deleting a symlink produces no event on some platforms (macOS
		// kqueue), so a dead mapping is detected lazily here: an event from
		// the target while the project-side symlink is gone means the
		// symlink (or a directory above it) was removed
		if viaSymlink != "" {
			if _, err := os.Lstat(viaSymlink); err != nil {
				w.handleGone(topmostGone(viaSymlink))
				continue
			}
		}
		if ignoreFile(logical) {
			continue
		}
		info, err := os.Stat(realPath)
		switch {
		case err != nil:
			w.handleGone(realPath, logical)
		case info.IsDir():
			w.handleDir(realPath, logical)
		default:
			w.handleFile(realPath, logical)
		}
	}
}

// topmostGone climbs from a nonexistent path to the highest ancestor that is
// also gone, so one removal event covers the whole deleted subtree. It
// returns the path twice (real and logical are the same for project-side
// paths).
func topmostGone(path string) (string, string) {
	gone := path
	for {
		idx := strings.LastIndex(gone, "/")
		if idx <= 0 {
			break
		}
		parent := gone[:idx]
		if _, err := os.Lstat(parent); err == nil {
			break
		}
		gone = parent
	}
	return gone, gone
}

// handleGone processes a path that no longer exists on disk
func (w *sourceWatcher) handleGone(realPath, logical string) {
	emit := false
	w.mu.Lock()
	if w.known[logical] {
		delete(w.known, logical)
		emit = true
	}
	wasDir := w.watchedDirs[realPath]
	if wasDir {
		w.forgetSubtree(realPath)
		emit = true
	}
	// Symlink mappings at or below the gone path. When the project-side
	// symlink itself is gone the mapping dies with it; when only the TARGET
	// is gone (transiently deleted by a tool) the mapping is kept so the
	// recreated target keeps syncing — a deleted directory target
	// additionally gets a recovery poller, since its watches died with it
	// and nothing watches its parent.
	for real, logicalTarget := range w.linkMap {
		if !isUnder(logicalTarget, logical) {
			continue
		}
		if _, err := os.Lstat(logicalTarget); err == nil {
			if real == realPath && wasDir && !w.recovering[real] {
				w.recovering[real] = true
				go w.recoverTarget(real, logicalTarget)
			}
			continue
		}
		w.fsw.Remove(real)
		delete(w.linkMap, real)
		delete(w.watchedDirs, real)
		emit = true
	}
	// Forget known files below the deleted path — only a directory or a
	// mapped subtree can have children, so plain file removals skip the sweep
	if wasDir || logical != realPath {
		for f := range w.known {
			if strings.HasPrefix(f, logical+"/") {
				delete(w.known, f)
			}
		}
	}
	// Watched roots at or below the gone path (a deleted project directory
	// takes its roots with it) are polled for recreation
	for _, root := range w.roots {
		if isUnder(root, realPath) && !w.missingRoots[root] {
			w.missingRoots[root] = true
			go w.recoverRoot(root)
		}
	}
	w.mu.Unlock()
	if emit && sourceTreeRegex.MatchString(logical) {
		handleFileChangeSafely(logical, "removed")
	}
}

// followIfSymlink handles realPath when it is a symlink, mirroring the
// startup scan: followed targets (external, with -s) are registered and
// their files emitted as added; every other symlink is skipped. Returns true
// when realPath was a symlink (handled either way).
func (w *sourceWatcher) followIfSymlink(realPath, logical string) bool {
	lst, err := os.Lstat(realPath)
	if err != nil || lst.Mode()&fs.ModeSymlink == 0 {
		return false
	}
	w.mu.Lock()
	files, _ := w.addSymlink(realPath, logical)
	w.mu.Unlock()
	w.emitAdded(files)
	return true
}

// handleDir registers a directory inside a watched tree and reports its
// files as added. Already-watched directories are re-registered too: a
// delete+recreate can collapse into a single event, leaving a dead kernel
// watch behind. The directory itself is not synced — it materializes in the
// deployed app when a file inside it syncs, and creating an empty directory
// should not force a redeploy.
func (w *sourceWatcher) handleDir(realPath, logical string) {
	if w.followIfSymlink(realPath, logical) {
		return
	}
	w.mu.Lock()
	var files []string
	// Only directories belonging to the project tree (directly, or mapped
	// through a followed symlink) are ours; siblings inside a watched
	// external parent are not
	if w.insideRoots(logical) || logical != realPath {
		files = w.addTree(realPath, logical)
	}
	w.mu.Unlock()
	w.emitAdded(files)
}

func (w *sourceWatcher) handleFile(realPath, logical string) {
	if w.followIfSymlink(realPath, logical) {
		return
	}
	if !sourceTreeRegex.MatchString(logical) {
		if opts.Verbose {
			fmt.Printf("Skipping event outside project source tree: %s\n", logical)
		}
		return
	}
	w.mu.Lock()
	event := "added"
	if w.known[logical] {
		event = "modified"
	}
	w.known[logical] = true
	w.mu.Unlock()
	handleFileChangeSafely(logical, event)
}

func (w *sourceWatcher) emitAdded(files []string) {
	for _, f := range files {
		if sourceTreeRegex.MatchString(f) {
			handleFileChangeSafely(f, "added")
		}
	}
}

// forgetSubtree drops the directory-watch bookkeeping for a removed
// directory; symlink mappings are handled separately by handleGone. Must be
// called with w.mu held.
func (w *sourceWatcher) forgetSubtree(realDir string) {
	for d := range w.watchedDirs {
		if isUnder(d, realDir) {
			delete(w.watchedDirs, d)
		}
	}
}

// recoverRoot waits for a deleted watched root (e.g. removed by a branch
// switch) to reappear and re-watches it: no ancestor is watched, so the
// recreation would otherwise go unnoticed until restart
func (w *sourceWatcher) recoverRoot(root string) {
	for {
		time.Sleep(2 * time.Second)
		w.mu.Lock()
		if w.stopped {
			w.mu.Unlock()
			return
		}
		if !dirExists(root) {
			w.mu.Unlock()
			continue
		}
		delete(w.missingRoots, root)
		files := w.addTree(root, root)
		w.mu.Unlock()
		if opts.Verbose {
			fmt.Printf("Re-watching restored directory %s\n", root)
		}
		w.emitAdded(files)
		return
	}
}

// recoverTarget waits for a deleted symlink target directory to reappear
// while the project-side symlink still exists, then re-watches it and syncs
// its files
func (w *sourceWatcher) recoverTarget(target, logical string) {
	for {
		time.Sleep(2 * time.Second)
		w.mu.Lock()
		if w.stopped {
			w.mu.Unlock()
			return
		}
		if _, err := os.Lstat(logical); err != nil {
			// the symlink itself is gone now: nothing to wait for
			delete(w.recovering, target)
			w.mu.Unlock()
			return
		}
		if !dirExists(target) {
			w.mu.Unlock()
			continue
		}
		delete(w.recovering, target)
		files := w.addTree(target, logical)
		w.mu.Unlock()
		if opts.Verbose {
			fmt.Printf("Re-watching restored symlink target %s\n", target)
		}
		w.emitAdded(files)
		return
	}
}
