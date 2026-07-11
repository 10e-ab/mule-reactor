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
	stopped bool
	flushMu sync.Mutex
}

func newDebouncer(delay time.Duration, flush func([]string)) *debouncer {
	return &debouncer{delay: delay, flush: flush, pending: map[string]bool{}}
}

func (d *debouncer) add(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.pending[path] = true
	if d.timer == nil {
		d.timer = time.AfterFunc(d.delay, d.fire)
	} else {
		d.timer.Reset(d.delay)
	}
}

// stop prevents any further flushes; an owner being replaced (watcher
// restart) must stop its debouncer so a pending batch cannot fire into the
// abandoned instance concurrently with its replacement
func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
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
	if d.stopped {
		d.mu.Unlock()
		return
	}
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
