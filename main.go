// MuleReactor is a hot-deployment tool for Mule applications: it watches Mule
// project source trees and syncs changed files into a deployed app's
// directory (under $MULE_HOME/apps or --apps-dir), triggering Mule's hot
// redeploy.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

type Options struct {
	Verbose           bool
	WatchPom          bool
	Notification      bool
	IgnoreFormatting  bool
	IgnoreWhitespace  bool
	IgnoreBlankLines  bool
	WatchDeployments  bool
	FollowSymlinks    bool
	ProjectsDir       string
	AppsDir           string
	ResourceFiltering bool
}

var opts Options

func main() {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	flag.BoolVar(&opts.Verbose, "v", false, "Run in verbose mode")
	flag.BoolVar(&opts.Verbose, "verbose", false, "Run in verbose mode")
	noNotification := flag.Bool("no-notification", false, "Disable desktop notifications (implies --no-watch-deployments)")
	noWatchDeployments := flag.Bool("no-watch-deployments", false, "Do not tail the server log for deployment success/failure notifications")
	noWatchPom := flag.Bool("no-watch-pom", false, "Do not rebuild when the pom.xml dependencies, properties, profiles or resources change; print a stale-app warning instead")
	noFollowSymlinks := flag.Bool("no-follow-symlinks", false, "Do not follow symbolic links pointing outside the project source trees")
	noResourceFiltering := flag.Bool("no-resource-filtering", false, "Disable Maven resource filtering: sync filtered resource files as-is, without substituting ${...} tokens. Pom property/profile/resources changes then no longer trigger rebuilds, only dependency changes do")
	noIgnoreFormatting := flag.Bool("no-ignore-formatting", false, "Compare XML/JSON contents exactly instead of canonicalizing formatting first")
	flag.BoolVar(&opts.IgnoreWhitespace, "ignore-whitespace", false, "Ignore whitespace changes within lines when comparing, in all file types (like diff -w). Off by default: whitespace can be meaningful in .properties or DataWeave files")
	flag.BoolVar(&opts.IgnoreBlankLines, "ignore-blank-lines", false, "Ignore added or removed blank lines when comparing, in all file types (like diff -B)")
	flag.StringVar(&opts.ProjectsDir, "projects-dir", wd, "Directory of projects (default: current directory)")
	flag.StringVar(&opts.AppsDir, "apps-dir", os.Getenv("MULE_HOME")+"/apps", "Directory to where the apps should be deployed (default: $MULE_HOME/apps)")
	flag.Parse()

	opts.Notification = !*noNotification
	opts.WatchDeployments = !*noWatchDeployments && opts.Notification
	opts.WatchPom = !*noWatchPom
	opts.FollowSymlinks = !*noFollowSymlinks
	opts.ResourceFiltering = !*noResourceFiltering
	opts.IgnoreFormatting = !*noIgnoreFormatting

	opts.ProjectsDir = normalizePath(opts.ProjectsDir)
	opts.AppsDir = normalizePath(opts.AppsDir)

	run()
}

// normalizePath makes a path absolute, cleaned and '/'-separated: all
// matching against watcher event paths assumes this canonical form, so
// relative --projects-dir/--apps-dir values must not leak past startup
func normalizePath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.ToSlash(filepath.Clean(p))
}

func run() {
	initNotifier()
	watchMuleAndResources()
	// The pom watcher rebuilds on relevant pom changes with -p/--watch-pom,
	// and warns about them without it
	watchPomFiles()
	if opts.WatchDeployments {
		watchDeployments()
	}

	fmt.Printf("Mule apps directory: %s\n", opts.AppsDir)
	fmt.Printf("Monitoring for changes in: %s. Press Ctrl+C to stop.\n", opts.ProjectsDir)
	if opts.Verbose {
		state := "disabled"
		if opts.FollowSymlinks {
			state = "enabled (native symlink target watching)"
		}
		fmt.Printf("Symlink following: %s\n", state)
	}
	select {}
}
