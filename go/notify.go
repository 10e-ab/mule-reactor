package main

import "os/exec"

// sendNotification shells out to a mule-reactor-notifier script expected on
// PATH (platform examples in notifiers/)
func sendNotification(title, message string) {
	exec.Command("mule-reactor-notifier", title, message).Run()
}
