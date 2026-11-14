package coding

import (
	"sync"
	"testing"
)

func TestTaskLockTableSerializesAndReclaims(t *testing.T) {
	table := newTaskLockTable()
	first := table.Lock("task-1")
	entered := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); unlock := table.Lock("task-1"); close(entered); <-release; unlock() }()
	if table.Len() != 1 {
		t.Fatalf("lock entries=%d", table.Len())
	}
	first()
	<-entered
	close(release)
	wg.Wait()
	if got := table.Len(); got != 0 {
		t.Fatalf("lock table leaked %d entries", got)
	}
}

func TestTaskLockTableDifferentTasksCanLockIndependently(t *testing.T) {
	table := newTaskLockTable()
	a := table.Lock("a")
	b := table.Lock("b")
	a()
	b()
	if table.Len() != 0 {
		t.Fatalf("lock table leaked entries: %d", table.Len())
	}
}

func TestTaskLockTableTryLockRejectsBusyKeyWithoutLeakingReference(t *testing.T) {
	table := newTaskLockTable()
	unlock := table.Lock("busy")
	if _, ok := table.TryLock("busy"); ok {
		t.Fatal("TryLock unexpectedly acquired a busy key")
	}
	if table.Len() != 1 {
		t.Fatalf("entry count after failed TryLock=%d", table.Len())
	}
	unlock()
	if table.Len() != 0 {
		t.Fatalf("entry leaked after release: %d", table.Len())
	}
}
