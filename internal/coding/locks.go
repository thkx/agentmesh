package coding

import "sync"

type taskLock struct {
	mu   sync.Mutex
	refs int // owners plus goroutines waiting for the lock
}
type taskLockTable struct {
	mu    sync.Mutex
	locks map[string]*taskLock
}

func newTaskLockTable() *taskLockTable { return &taskLockTable{locks: make(map[string]*taskLock)} }
func (t *taskLockTable) acquireEntry(id string) *taskLock {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.locks[id]
	if entry == nil {
		entry = &taskLock{}
		t.locks[id] = entry
	}
	entry.refs++
	return entry
}
func (t *taskLockTable) releaseEntry(id string, entry *taskLock) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu.Unlock()
			t.mu.Lock()
			entry.refs--
			if entry.refs == 0 && t.locks[id] == entry {
				delete(t.locks, id)
			}
			t.mu.Unlock()
		})
	}
}
func (t *taskLockTable) Lock(id string) func() {
	entry := t.acquireEntry(id)
	entry.mu.Lock()
	return t.releaseEntry(id, entry)
}

// TryLock acquires without waiting. Mutating API operations use it so a
// request arriving during execution is rejected instead of queued until the
// task has already completed.
func (t *taskLockTable) TryLock(id string) (func(), bool) {
	entry := t.acquireEntry(id)
	if !entry.mu.TryLock() {
		t.mu.Lock()
		entry.refs--
		if entry.refs == 0 && t.locks[id] == entry {
			delete(t.locks, id)
		}
		t.mu.Unlock()
		return nil, false
	}
	return t.releaseEntry(id, entry), true
}
func (t *taskLockTable) Len() int { t.mu.Lock(); defer t.mu.Unlock(); return len(t.locks) }
