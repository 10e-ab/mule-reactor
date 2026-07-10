package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
)

var (
	startedAppRegex   = regexp.MustCompile(`\* Started app ['"]([^'"]+)['"]`)
	deployFailedRegex = regexp.MustCompile(`DeploymentException: Failed to deploy artifact \[([^\]]+)\]`)
)

func watchDeployments() {
	if opts.Verbose {
		fmt.Println("Deployment watching enabled")
	}
	logFilePath := opts.AppsDir + "/../logs/mule_ee.log"
	go tailFile(logFilePath, func(line string) {
		if m := startedAppRegex.FindStringSubmatch(line); m != nil {
			if opts.Notification {
				sendNotification("✅", "Deployment: "+m[1]+" succeeded")
			}
		} else if m := deployFailedRegex.FindStringSubmatch(line); m != nil {
			if opts.Notification {
				sendNotification("❌", "Deployment: "+m[1]+" failed")
			}
		}
	})
}

// tailFile follows a file like `tail -F`: across truncation and rotation,
// retrying if the file doesn't exist yet
func tailFile(path string, onLine func(string)) {
	var file *os.File
	var reader *bufio.Reader
	var offset int64
	firstOpen := true

	openFile := func() bool {
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		offset = 0
		if firstOpen {
			// Like tail, only report lines appended from now on
			if off, err := f.Seek(0, io.SeekEnd); err == nil {
				offset = off
			}
		}
		firstOpen = false
		file = f
		reader = bufio.NewReader(f)
		return true
	}

	for {
		if file == nil {
			if !openFile() {
				time.Sleep(time.Second)
				continue
			}
		}
		line, err := reader.ReadString('\n')
		if err == nil {
			offset += int64(len(line))
			onLine(line)
			continue
		}
		if len(line) > 0 {
			// A partial line without its newline yet: rewind so it is
			// re-read whole once the rest arrives
			file.Seek(offset, io.SeekStart)
			reader.Reset(file)
		}
		time.Sleep(500 * time.Millisecond)
		info, statErr := os.Stat(path)
		fileInfo, fstatErr := file.Stat()
		rotated := statErr == nil && fstatErr == nil && !os.SameFile(info, fileInfo)
		truncated := statErr == nil && info.Size() < offset
		if statErr != nil || rotated || truncated {
			file.Close()
			file = nil
			reader = nil
		}
	}
}
