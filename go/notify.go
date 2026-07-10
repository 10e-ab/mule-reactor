package main

import (
	"os/exec"
	"runtime"
)

// sendNotification shells out to a mule-reactor-notifier script expected on
// PATH (platform examples in notifiers/). It must go through a shell, like
// the Ruby version's `system` call: notifier scripts without a shebang run
// fine from a shell but are rejected by a direct exec. Title and message are
// passed as positional parameters so their content is never shell-parsed.
func sendNotification(title, message string) {
	if runtime.GOOS == "windows" {
		exec.Command("cmd", "/C", "mule-reactor-notifier", title, message).Run()
		return
	}
	exec.Command("sh", "-c", `mule-reactor-notifier "$1" "$2"`, "sh", title, message).Run()
}
