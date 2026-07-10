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
	flag.BoolVar(&opts.Notification, "n", false, "Enable notifications")
	flag.BoolVar(&opts.Notification, "notification", false, "Enable notifications")
	flag.BoolVar(&opts.WatchDeployments, "d", false, "Will tail the server-log and notify on deployment status. Notification must be enabled")
	flag.BoolVar(&opts.WatchDeployments, "watch-deployments", false, "Will tail the server-log and notify on deployment status. Notification must be enabled")
	flag.BoolVar(&opts.WatchPom, "p", false, "Rebuild when the pom.xml dependencies, properties, profiles or resources change. Without this flag a warning is printed instead")
	flag.BoolVar(&opts.WatchPom, "watch-pom", false, "Rebuild when the pom.xml dependencies, properties, profiles or resources change. Without this flag a warning is printed instead")
	flag.BoolVar(&opts.FollowSymlinks, "s", false, "Follow symbolic links by watching the symlink targets directly")
	flag.BoolVar(&opts.FollowSymlinks, "follow-symlinks", false, "Follow symbolic links by watching the symlink targets directly")
	noResourceFiltering := flag.Bool("no-resource-filtering", false, "Disable Maven resource filtering: sync filtered resource files as-is, without substituting ${...} tokens. Pom property/profile/resources changes then no longer trigger rebuilds, only dependency changes do")
	noIgnoreFormatting := flag.Bool("no-ignore-formatting", false, "Considers changes in formatting for JSON and XML files")
	noIgnoreWhitespace := flag.Bool("no-ignore-whitespace", false, "Considers whitespace changes in all file types")
	noIgnoreBlankLines := flag.Bool("no-ignore-blank-lines", false, "Considers blank lines in all file types during comparison")
	flag.StringVar(&opts.ProjectsDir, "projects-dir", wd, "Directory of projects (default: current directory)")
	flag.StringVar(&opts.AppsDir, "apps-dir", os.Getenv("MULE_HOME")+"/apps", "Directory to where the apps should be deployed (default: $MULE_HOME/apps)")
	flag.Parse()

	opts.ResourceFiltering = !*noResourceFiltering
	opts.IgnoreFormatting = !*noIgnoreFormatting
	opts.IgnoreWhitespace = !*noIgnoreWhitespace
	opts.IgnoreBlankLines = !*noIgnoreBlankLines

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
	if opts.WatchDeployments && opts.Notification {
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
