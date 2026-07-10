package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/gen2brain/beeep"
)

// notifierScript is the resolved shell-out target; empty means the built-in
// notifier is used. Written once by initNotifier before the watchers start.
var notifierScript string

// initNotifier resolves how notifications are delivered, once at startup:
// an explicit MULE_REACTOR_NOTIFIER script, a mule-reactor-notifier found on
// PATH, or the built-in cross-platform notifier. Resolving up front (and
// saying which one is active) matters because notifier failures are
// otherwise invisible.
func initNotifier() {
	if !opts.Notification {
		return
	}
	if custom := os.Getenv("MULE_REACTOR_NOTIFIER"); custom != "" {
		if _, err := exec.LookPath(custom); err != nil {
			fmt.Printf("WARNING: MULE_REACTOR_NOTIFIER=%s was not found or is not executable, notifications may fail\n", custom)
		}
		notifierScript = custom
		fmt.Printf("Notifications: using %s (from MULE_REACTOR_NOTIFIER)\n", custom)
		return
	}
	if path, err := exec.LookPath("mule-reactor-notifier"); err == nil {
		notifierScript = "mule-reactor-notifier"
		fmt.Printf("Notifications: using mule-reactor-notifier from PATH (%s)\n", path)
		return
	}
	fmt.Println("Notifications: using the built-in notifier (put a mule-reactor-notifier script on PATH to customize)")
}

func sendNotification(title, message string) {
	if !opts.Notification {
		return
	}
	if notifierScript != "" {
		runNotifierScript(notifierScript, title, message)
		return
	}
	if err := beeep.Notify(title, message, ""); err != nil && opts.Verbose {
		fmt.Printf("WARNING: notification failed: %v\n", err)
	}
}

// runNotifierScript must go through a shell: notifier scripts without a
// shebang run fine from a shell but are rejected by a direct exec. The
// script path and both arguments are passed as positional parameters so
// their content is never shell-parsed.
func runNotifierScript(script, title, message string) {
	if runtime.GOOS == "windows" {
		exec.Command("cmd", "/C", script, title, message).Run()
		return
	}
	exec.Command("sh", "-c", `"$0" "$1" "$2"`, script, title, message).Run()
}
