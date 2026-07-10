package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Editors typically fire several events per save (write + rename + chmod);
// changes are collected until the burst quiets down and then handled as one
// batch
const debounceDelay = 300 * time.Millisecond

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
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if dirExists(m) {
				dirs = append(dirs, filepath.ToSlash(m))
			}
		}
	}
	if opts.Verbose {
		fmt.Printf("Watching directories : %v\n", dirs)
	}
	return dirs
}

type sourceWatcher struct {
	fsw *fsnotify.Watcher
	mu  sync.Mutex
	// files already seen, by logical path: distinguishes "added" from
	// "modified", since editors often save via create+rename
	known map[string]bool
	// watched real path → logical path; differs from identity only for
	// symlink targets watched with -s/--follow-symlinks
	watchedDirs map[string]string
	pending     map[string]bool
	timer       *time.Timer
}

func watchMuleAndResources() {
	if !dirExists(opts.AppsDir) {
		fmt.Printf("Directory %s does not exist\n", opts.AppsDir)
		fmt.Println("Make sure that MULE_HOME is set and points to an existing mule-server directory, or specify the --apps-dir script option")
		os.Exit(1)
	}
	if !dirExists(opts.ProjectsDir) {
		fmt.Printf("Directory %s does not exist\n", opts.ProjectsDir)
		fmt.Println("Make sure that --project-dir points to an existing directory")
		os.Exit(1)
	}

	dirs := watchDirs(opts.ProjectsDir)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("ERROR: could not start file watcher: %v\n", err)
		os.Exit(1)
	}
	w := &sourceWatcher{
		fsw:         fsw,
		known:       map[string]bool{},
		watchedDirs: map[string]string{},
		pending:     map[string]bool{},
	}
	for _, dir := range dirs {
		w.addTree(dir, dir, nil)
	}
	go w.eventLoop()
}

// addTree watches realDir and everything below it. logicalDir is the path as
// seen from the project tree; it differs from realDir below external
// symlinks, whose resolved targets are watched directly so changes behind
// them produce native events. Files discovered while adding are appended to
// newFiles (when non-nil) so new directories can report their contents as
// added.
func (w *sourceWatcher) addTree(realDir, logicalDir string, newFiles *[]string) {
	if err := w.fsw.Add(realDir); err != nil {
		fmt.Printf("WARNING: could not watch %s: %v\n", realDir, err)
		return
	}
	w.watchedDirs[realDir] = logicalDir
	entries, err := os.ReadDir(realDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		realPath := realDir + "/" + entry.Name()
		logicalPath := logicalDir + "/" + entry.Name()
		if entry.Type()&fs.ModeSymlink != 0 {
			if !opts.FollowSymlinks {
				continue
			}
			target, err := filepath.EvalSymlinks(realPath)
			if err != nil {
				continue
			}
			target = filepath.ToSlash(target)
			info, err := os.Stat(target)
			if err != nil {
				continue
			}
			if info.IsDir() {
				if opts.Verbose {
					fmt.Printf("Following symlinked directory: %s -> %s\n", logicalPath, target)
				}
				w.addTree(target, logicalPath, newFiles)
			} else {
				// A symlink to a single external file: watch the target itself
				if err := w.fsw.Add(target); err == nil {
					w.watchedDirs[target] = logicalPath
				}
				w.known[logicalPath] = true
			}
		} else if entry.IsDir() {
			w.addTree(realPath, logicalPath, newFiles)
		} else {
			if !w.known[logicalPath] && newFiles != nil {
				*newFiles = append(*newFiles, logicalPath)
			}
			w.known[logicalPath] = true
		}
	}
}

// toLogical maps a real event path back into the project tree when the event
// came from a watched symlink target. Must be called with w.mu held.
func (w *sourceWatcher) toLogical(realPath string) string {
	best := ""
	bestLogical := ""
	for real, logical := range w.watchedDirs {
		if real == logical {
			continue
		}
		if realPath == real || strings.HasPrefix(realPath, real+"/") {
			if len(real) > len(best) {
				best = real
				bestLogical = logical
			}
		}
	}
	if best == "" {
		return realPath
	}
	return bestLogical + strings.TrimPrefix(realPath, best)
}

func (w *sourceWatcher) eventLoop() {
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if ev.Op == fsnotify.Chmod {
				continue
			}
			w.mu.Lock()
			w.pending[filepath.ToSlash(ev.Name)] = true
			if w.timer == nil {
				w.timer = time.AfterFunc(debounceDelay, w.flush)
			} else {
				w.timer.Reset(debounceDelay)
			}
			w.mu.Unlock()
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			fmt.Printf("ERROR: file watcher: %v\n", err)
		}
	}
}

func (w *sourceWatcher) flush() {
	w.mu.Lock()
	paths := make([]string, 0, len(w.pending))
	for p := range w.pending {
		paths = append(paths, p)
	}
	w.pending = map[string]bool{}
	w.timer = nil
	w.mu.Unlock()
	sort.Strings(paths)

	if opts.Verbose {
		fmt.Println("Changes detected")
	}
	for _, realPath := range paths {
		w.mu.Lock()
		logical := w.toLogical(realPath)
		w.mu.Unlock()
		if ignoreFile(logical) {
			continue
		}
		info, err := os.Stat(realPath)
		switch {
		case err != nil:
			// Gone: a removed file or directory
			event := ""
			w.mu.Lock()
			if w.known[logical] {
				delete(w.known, logical)
				event = "removed"
			} else if _, wasDir := w.watchedDirs[realPath]; wasDir {
				delete(w.watchedDirs, realPath)
				// Forget the files that lived under the removed directory
				for f := range w.known {
					if strings.HasPrefix(f, logical+"/") {
						delete(w.known, f)
					}
				}
				event = "removed"
			}
			w.mu.Unlock()
			if event != "" {
				handleFileChangeSafely(logical, event)
			}
		case info.IsDir():
			w.mu.Lock()
			_, watched := w.watchedDirs[realPath]
			var newFiles []string
			if !watched {
				w.addTree(realPath, logical, &newFiles)
			}
			w.mu.Unlock()
			if !watched {
				handleFileChangeSafely(logical, "added")
				for _, f := range newFiles {
					handleFileChangeSafely(f, "added")
				}
			}
		default:
			w.mu.Lock()
			event := "added"
			if w.known[logical] {
				event = "modified"
			}
			w.known[logical] = true
			w.mu.Unlock()
			handleFileChangeSafely(logical, event)
		}
	}
}
