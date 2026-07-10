package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// debouncer collects paths from a burst of file events and hands them to
// flush as one sorted batch once the burst has been quiet for delay.
// Batches are serialized — a new batch waits for the previous flush to
// finish, so two flushes can never process the same app concurrently — and a
// panic in flush is contained so the watcher keeps running.
type debouncer struct {
	delay   time.Duration
	flush   func(paths []string)
	mu      sync.Mutex
	pending map[string]bool
	timer   *time.Timer
	flushMu sync.Mutex
}

func newDebouncer(delay time.Duration, flush func([]string)) *debouncer {
	return &debouncer{delay: delay, flush: flush, pending: map[string]bool{}}
}

func (d *debouncer) add(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending[path] = true
	if d.timer == nil {
		d.timer = time.AfterFunc(d.delay, d.fire)
	} else {
		d.timer.Reset(d.delay)
	}
}

func (d *debouncer) fire() {
	d.flushMu.Lock()
	defer d.flushMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("ERROR processing change batch: %v\n", r)
		}
	}()
	d.mu.Lock()
	paths := make([]string, 0, len(d.pending))
	for p := range d.pending {
		paths = append(paths, p)
	}
	d.pending = map[string]bool{}
	d.timer = nil
	d.mu.Unlock()
	if len(paths) == 0 {
		return
	}
	sort.Strings(paths)
	d.flush(paths)
}
