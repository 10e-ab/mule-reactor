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
			if dirExists(m) {
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
	if err := startSourceWatcher(); err != nil {
		fmt.Printf("ERROR: could not start file watcher: %v\n", err)
		os.Exit(1)
	}
}

func startSourceWatcher() error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w := &sourceWatcher{
		fsw:          fsw,
		roots:        watchDirs(opts.ProjectsDir),
		known:        map[string]bool{},
		watchedDirs:  map[string]bool{},
		linkMap:      map[string]string{},
		missingRoots: map[string]bool{},
	}
	w.deb = newDebouncer(debounceDelay, w.processBatch)
	w.mu.Lock()
	for _, dir := range w.roots {
		w.addTree(dir, dir)
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
		if path == root || strings.HasPrefix(path, root+"/") {
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
		if realPath == real || strings.HasPrefix(realPath, real+"/") {
			if len(real) > len(best) {
				best, bestLogical = real, logical
			}
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
// backend failure degrades to a re-scan instead of silently stopping syncs
func (w *sourceWatcher) restart() {
	fmt.Println("ERROR: file watcher stopped, restarting...")
	w.fsw.Close()
	for {
		if err := startSourceWatcher(); err == nil {
			return
		}
		time.Sleep(5 * time.Second)
	}
}

func (w *sourceWatcher) processBatch(paths []string) {
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
	if w.watchedDirs[realPath] {
		w.forgetSubtree(realPath, logical)
		emit = true
	}
	// A deleted symlink leaves no known/watchedDirs entry under its own
	// path — its state is keyed by the resolved target — so drop every
	// mapping whose logical side lived at or below the deleted path
	for real, logicalTarget := range w.linkMap {
		if logicalTarget == logical || strings.HasPrefix(logicalTarget, logical+"/") {
			w.fsw.Remove(real)
			delete(w.linkMap, real)
			delete(w.watchedDirs, real)
			emit = true
		}
	}
	for f := range w.known {
		if strings.HasPrefix(f, logical+"/") {
			delete(w.known, f)
		}
	}
	for _, root := range w.roots {
		if realPath == root && !w.missingRoots[root] {
			w.missingRoots[root] = true
			go w.recoverRoot(root)
		}
	}
	w.mu.Unlock()
	if emit && sourceTreeRegex.MatchString(logical) {
		handleFileChangeSafely(logical, "removed")
	}
}

// handleDir registers a directory that appeared inside a watched tree and
// reports its files as added. The directory itself is not synced: it
// materializes in the deployed app when a file inside it syncs, and creating
// an empty directory should not force a redeploy.
func (w *sourceWatcher) handleDir(realPath, logical string) {
	if lst, err := os.Lstat(realPath); err == nil && lst.Mode()&fs.ModeSymlink != 0 {
		w.mu.Lock()
		files, followed := w.addSymlink(realPath, logical)
		w.mu.Unlock()
		if followed {
			w.emitAdded(files)
			return
		}
	}
	w.mu.Lock()
	var files []string
	// Only directories that belong to the project tree (directly, or mapped
	// through a followed symlink) are picked up; siblings inside a watched
	// external parent are not ours
	if !w.watchedDirs[realPath] && (w.insideRoots(logical) || logical != realPath) {
		files = w.addTree(realPath, logical)
	}
	w.mu.Unlock()
	w.emitAdded(files)
}

func (w *sourceWatcher) handleFile(realPath, logical string) {
	// A symlink created at runtime is followed like those found at startup
	if lst, err := os.Lstat(realPath); err == nil && lst.Mode()&fs.ModeSymlink != 0 {
		w.mu.Lock()
		files, followed := w.addSymlink(realPath, logical)
		w.mu.Unlock()
		if followed {
			w.emitAdded(files)
			return
		}
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

// forgetSubtree drops the bookkeeping for a removed directory. Must be
// called with w.mu held.
func (w *sourceWatcher) forgetSubtree(realDir, logicalDir string) {
	for d := range w.watchedDirs {
		if d == realDir || strings.HasPrefix(d, realDir+"/") {
			delete(w.watchedDirs, d)
		}
	}
	for real, logical := range w.linkMap {
		if real == realDir || strings.HasPrefix(real, realDir+"/") ||
			logical == logicalDir || strings.HasPrefix(logical, logicalDir+"/") {
			delete(w.linkMap, real)
		}
	}
}

// recoverRoot waits for a deleted watched root (e.g. removed by a branch
// switch) to reappear and re-watches it: no ancestor is watched, so the
// recreation would otherwise go unnoticed until restart
func (w *sourceWatcher) recoverRoot(root string) {
	for {
		time.Sleep(2 * time.Second)
		if !dirExists(root) {
			continue
		}
		w.mu.Lock()
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
