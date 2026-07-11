package main

import (
	"reflect"
	"testing"
)

func TestGlobVisible(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/proj/src/main/mule/f.xml", "")
	writeFile(t, dir+"/.hidden/src/main/mule/f.xml", "")
	writeFile(t, dir+"/other/src/main/mule/f.xml", "")

	got := globVisible(dir+"/*/src/main/mule", dir)
	want := []string{dir + "/other/src/main/mule", dir + "/proj/src/main/mule"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("globVisible = %v, want %v (hidden dirs excluded)", got, want)
	}
}

func TestWatchDirs(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/app/src/main/mule/f.xml", "")
	writeFile(t, dir+"/app/src/main/resources/p.properties", "")
	writeFile(t, dir+"/src/main/mule/g.xml", "") // projects dir IS a project
	writeFile(t, dir+"/noise/readme.md", "")

	dirs := watchDirs(dir)
	want := []string{
		dir + "/app/src/main/mule",
		dir + "/src/main/mule",
		dir + "/app/src/main/resources",
	}
	if !reflect.DeepEqual(dirs, want) {
		t.Errorf("watchDirs = %v, want %v", dirs, want)
	}
}

func TestTopmostGone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/exists/keep.txt", "")

	gone, logical := topmostGone(dir + "/exists/a/b/c")
	if gone != dir+"/exists/a" || logical != gone {
		t.Errorf("topmostGone = %q, want %q", gone, dir+"/exists/a")
	}

	// a path whose immediate parent exists stays put
	gone, _ = topmostGone(dir + "/exists/missing.txt")
	if gone != dir+"/exists/missing.txt" {
		t.Errorf("topmostGone = %q, want the path itself", gone)
	}
}

func TestSourceTreeRegex(t *testing.T) {
	if !sourceTreeRegex.MatchString("/p/src/main/mule/f.xml") {
		t.Error("mule tree path must match")
	}
	if !sourceTreeRegex.MatchString("/p/src/main/resources/a/b.txt") {
		t.Error("resources tree path must match")
	}
	if sourceTreeRegex.MatchString("/ext/config/shared.properties") {
		t.Error("external sibling paths must not match")
	}
	if sourceTreeRegex.MatchString("/p/src/main/mule") {
		t.Error("the watch root itself is not a syncable path")
	}
}
