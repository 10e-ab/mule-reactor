package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type pomState struct {
	hash       string
	content    string
	filterHash string
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// hashDependencies hashes the dependency and parent sections of a pom, and
// returns a formatted rendering of them for verbose diff output
func hashDependencies(root *XMLNode) (string, string) {
	var dependenciesText, parentText, formatted strings.Builder

	for _, dependency := range root.descendants("dependency") {
		formatted.WriteString("\n<dependency>\n")
		for _, c := range sortedCanonicalChildren(dependency) {
			formatted.WriteString("  " + c + "\n")
			dependenciesText.WriteString(c)
		}
		formatted.WriteString("</dependency>\n")
	}

	// Since dependencies can be defined in the parent pom we include any changes to the parent
	for _, parent := range root.descendants("parent") {
		formatted.WriteString("\n<parent>\n")
		for _, c := range sortedCanonicalChildren(parent) {
			formatted.WriteString("  " + c + "\n")
			parentText.WriteString(c)
		}
		formatted.WriteString("</parent>\n")
	}

	return sha256Hex(dependenciesText.String() + parentText.String()), formatted.String()
}

func sortedCanonicalChildren(n *XMLNode) []string {
	var out []string
	for i := range n.Children {
		out = append(out, n.Children[i].canonical())
	}
	sort.Strings(out)
	return out
}

// hashFilterInputs hashes the pom content that affects resource filtering, so
// the pom watcher can detect when filtered resources need a re-sync
func hashFilterInputs(root *XMLNode) string {
	var content strings.Builder
	for _, path := range [][]string{{"properties"}, {"profiles"}, {"build", "resources"}} {
		if node := root.find(path...); node != nil {
			content.WriteString(node.canonical())
		}
	}
	return sha256Hex(content.String())
}

func pomFileState(data []byte) (pomState, error) {
	root, err := parseXML(data)
	if err != nil {
		return pomState{}, err
	}
	hash, content := hashDependencies(root)
	return pomState{hash: hash, content: content, filterHash: hashFilterInputs(root)}, nil
}

// A pom change is rebuild-worthy when the dependencies/parent changed, or —
// with resource filtering enabled — when the filtering inputs (properties,
// profiles, resources) changed. Properties count because they can feed
// dependency versions and filtered resource files alike; a full rebuild is the
// one response that is correct in every case. With --no-resource-filtering the
// pom properties never reach the deployed app, so they are not watched.
func pomRebuildWorthy(oldState, newState pomState) bool {
	if oldState.hash != newState.hash {
		return true
	}
	return opts.ResourceFiltering && oldState.filterHash != newState.filterHash
}

func initializePomState(projectsDir string) map[string]pomState {
	states := map[string]pomState{}
	pd := removeTrailingSlash(projectsDir)
	for _, pattern := range []string{pd + "/pom.xml", pd + "/*/pom.xml"} {
		matches, _ := filepath.Glob(pattern)
		for _, pomFile := range matches {
			pomFile = filepath.ToSlash(pomFile)
			if ignoreFile(pomFile) {
				continue
			}
			data, err := os.ReadFile(pomFile)
			if err != nil {
				continue
			}
			if state, err := pomFileState(data); err == nil {
				states[pomFile] = state
			}
		}
	}
	return states
}

// watchPomFiles watches the pom.xml files of each project. Unlike the Ruby
// version there is no polling: fsnotify watches are per-directory and
// non-recursive, so only the projects dir and each project root are watched
// and the target/ event storm from `mvn clean package` never reaches us.
func watchPomFiles() {
	if opts.Verbose {
		fmt.Println("Tracking changes in pom.xml files")
	}
	pd := removeTrailingSlash(opts.ProjectsDir)
	states := initializePomState(pd)
	var statesMu sync.Mutex

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("ERROR: could not start pom watcher: %v\n", err)
		return
	}
	watchList := []string{pd}
	entries, _ := os.ReadDir(pd)
	for _, e := range entries {
		if e.IsDir() && !ignoreFile(pd+"/"+e.Name()+"/") {
			watchList = append(watchList, pd+"/"+e.Name())
		}
	}
	for _, d := range watchList {
		if err := fsw.Add(d); err != nil && opts.Verbose {
			fmt.Printf("WARNING: could not watch %s: %v\n", d, err)
		}
	}

	pending := map[string]bool{}
	var mu sync.Mutex
	var timer *time.Timer

	process := func() {
		mu.Lock()
		batch := make([]string, 0, len(pending))
		for f := range pending {
			batch = append(batch, f)
		}
		pending = map[string]bool{}
		timer = nil
		mu.Unlock()
		sort.Strings(batch)
		if opts.Verbose {
			fmt.Printf("[%s] POM changes detected: %s\n", time.Now().Format(time.RFC3339), strings.Join(batch, ", "))
		}
		for _, filename := range batch {
			processPomChange(filename, states, &statesMu)
		}
	}

	go func() {
		for {
			select {
			case ev, ok := <-fsw.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) != "pom.xml" || ev.Op == fsnotify.Chmod {
					continue
				}
				mu.Lock()
				pending[filepath.ToSlash(ev.Name)] = true
				if timer == nil {
					timer = time.AfterFunc(debounceDelay, process)
				} else {
					timer.Reset(debounceDelay)
				}
				mu.Unlock()
			case err, ok := <-fsw.Errors:
				if !ok {
					return
				}
				fmt.Printf("ERROR: pom watcher: %v\n", err)
			}
		}
	}()
}

func processPomChange(filename string, states map[string]pomState, statesMu *sync.Mutex) {
	// Never let one pom's failure abort the rest of a change batch
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("ERROR processing POM file %s: %v\n", filename, r)
		}
	}()
	if ignoreFile(filename) {
		return
	}
	if !fileExists(filename) {
		fmt.Println("NOT IMPLEMENTED: POM file deleted")
		return
	}
	projectName := extractProjectNameFromPom(filename, true)
	if projectName == "" {
		// e.g. an aggregator/parent pom, not a deployable app
		if opts.Verbose {
			fmt.Printf("Skipping pom without <name>: %s\n", filename)
		}
		return
	}
	appDir := opts.AppsDir + "/" + projectName
	if !appDeployed(appDir) {
		return
	}
	fmt.Println("POM file updated")
	data, err := os.ReadFile(filename)
	if err != nil {
		fmt.Printf("ERROR processing POM file %s: %v\n", filename, err)
		return
	}
	newState, err := pomFileState(data)
	if err != nil {
		fmt.Printf("ERROR processing POM file %s: %v\n", filename, err)
		return
	}
	statesMu.Lock()
	oldState, tracked := states[filename]
	states[filename] = newState
	statesMu.Unlock()
	if !tracked {
		fmt.Printf("Change tracking new pom file: %s\n", filename)
		return
	}
	if opts.Verbose {
		fmt.Printf("Last pom state hash: %s\n", oldState.hash)
	}
	if !pomRebuildWorthy(oldState, newState) {
		return
	}
	fmt.Printf("Change detected in dependencies/properties/profiles/resources of pom file: %s\n", filename)
	if opts.Verbose {
		fmt.Printf("POM state before:\n%s\nPOM state after:\n%s\n", oldState.content, newState.content)
	}
	if opts.WatchPom {
		// A full rebuild resolves new dependencies and re-applies Maven resource filtering
		rebuildProject(filename)
	} else {
		fmt.Println("WARNING: The pom change requires a rebuild, the deployed app is now stale. Start with -p/--watch-pom to rebuild automatically, or rebuild manually.")
	}
}

func rebuildProject(pomFile string) {
	projectRoot := filepath.Dir(pomFile)
	projectName := extractProjectNameFromPom(pomFile, false)
	if opts.Notification {
		sendNotification("🛠️", "Rebuilding: "+projectName)
	}
	// Unlike the Ruby version this doesn't go through a shell, so it also
	// works on Windows. TODO: Make the build command configurable
	cmd := exec.Command("mvn", "clean", "package", "-DskipTests")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		exitStatus := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitStatus = exitErr.ExitCode()
		}
		fmt.Printf("Maven build failed with exit status %d\n", exitStatus)
		if opts.Notification {
			sendNotification("🛠️ ❌", "Build: "+projectName+" failed")
		}
		return
	}
	jars, _ := filepath.Glob(filepath.Join(projectRoot, "target", projectName+"*.jar"))
	copied := false
	for _, jar := range jars {
		if err := copyFile(jar, opts.AppsDir+"/"+projectName+".jar"); err != nil {
			fmt.Printf("ERROR copying %s: %v\n", jar, err)
		} else {
			copied = true
		}
	}
	if copied {
		fmt.Printf("Maven build executed successfully, redeploying app %s\n", projectName)
		if opts.Notification {
			sendNotification("🛠️ ✅", "Build: "+projectName+" succeeded")
		}
	} else {
		fmt.Printf("Maven build succeeded but no target/%s*.jar could be copied\n", projectName)
		if opts.Notification {
			sendNotification("🛠️ ❌", "Build: "+projectName+" failed")
		}
	}
}
