package main

import (
	"os"
	"path/filepath"
	"testing"
)

// setOpts installs test options and restores the previous ones afterwards.
// Tests share the package-global opts, so none of them may use t.Parallel.
func setOpts(t *testing.T, o Options) {
	t.Helper()
	prev := opts
	opts = o
	t.Cleanup(func() { opts = prev })
}

func defaultTestOpts() Options {
	return Options{
		ResourceFiltering: true,
		IgnoreFormatting:  true,
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
