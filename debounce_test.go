package main

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDebouncerBatchesAndSorts(t *testing.T) {
	var mu sync.Mutex
	var batches [][]string
	d := newDebouncer(20*time.Millisecond, func(paths []string) {
		mu.Lock()
		batches = append(batches, paths)
		mu.Unlock()
	})
	d.add("b")
	d.add("a")
	d.add("b") // duplicate collapses
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}
	if !reflect.DeepEqual(batches[0], []string{"a", "b"}) {
		t.Errorf("batch = %v, want [a b]", batches[0])
	}
}

func TestDebouncerSerializesFlushes(t *testing.T) {
	var mu sync.Mutex
	active, maxActive, flushes := 0, 0, 0
	d := newDebouncer(10*time.Millisecond, func(paths []string) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		active--
		flushes++
		mu.Unlock()
	})
	d.add("a")
	time.Sleep(25 * time.Millisecond) // first flush is now running
	d.add("b")                        // must wait for it
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if maxActive != 1 {
		t.Errorf("flushes overlapped: max concurrent = %d", maxActive)
	}
	if flushes != 2 {
		t.Errorf("flushes = %d, want 2", flushes)
	}
}

func TestDebouncerRecoversFromPanic(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	d := newDebouncer(10*time.Millisecond, func(paths []string) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			panic("boom")
		}
	})
	d.add("a")
	time.Sleep(50 * time.Millisecond)
	d.add("b")
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("a panicking flush must not kill the debouncer: calls = %d, want 2", calls)
	}
}
