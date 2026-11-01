package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrInvalidWorkspaceID = errors.New("invalid workspace id")
	ErrWorkspaceInUse     = errors.New("workspace is in use")
)

type Workspace struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Repository string `json:"repository,omitempty"`
}
type Manager struct {
	Root   string
	mu     sync.Mutex
	leases map[string]int
}

func NewManager(root string) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	return &Manager{Root: abs, leases: make(map[string]int)}, nil
}
func (m *Manager) Create(ctx context.Context, id string) (Workspace, error) {
	if err := ctx.Err(); err != nil {
		return Workspace{}, err
	}
	if !validID(id) {
		return Workspace{}, ErrInvalidWorkspaceID
	}
	path := filepath.Join(m.Root, id)
	if err := os.Mkdir(path, 0700); err != nil {
		return Workspace{}, err
	}
	return Workspace{ID: id, Path: path}, nil
}
func (m *Manager) Remove(id string) error {
	if !validID(id) {
		return ErrInvalidWorkspaceID
	}
	path := filepath.Join(m.Root, id)
	m.mu.Lock()
	defer m.mu.Unlock()
	leaseKey := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		leaseKey = resolved
	}
	if m.leases[leaseKey] > 0 {
		return ErrWorkspaceInUse
	}
	return os.RemoveAll(path)
}

// AcquirePath protects a managed workspace from deletion while an execution uses it.
func (m *Manager) AcquirePath(path string) (func(), error) {
	m.mu.Lock()
	abs, err := filepath.Abs(path)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	root, err := filepath.EvalSymlinks(m.Root)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		m.mu.Unlock()
		return nil, ErrInvalidWorkspaceID
	}
	info, err := os.Stat(resolved)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !info.IsDir() {
		m.mu.Unlock()
		return nil, errors.New("workspace path is not a directory")
	}
	m.leases[resolved]++
	m.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.leases[resolved] <= 1 {
				delete(m.leases, resolved)
			} else {
				m.leases[resolved]--
			}
		})
	}
	return release, nil
}
func validID(id string) bool {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Resolve returns an existing managed workspace by ID without exposing arbitrary filesystem paths.
func (m *Manager) Resolve(id string) (Workspace, error) {
	if !validID(id) {
		return Workspace{}, ErrInvalidWorkspaceID
	}
	path := filepath.Join(m.Root, id)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Workspace{}, err
	}
	root, err := filepath.EvalSymlinks(m.Root)
	if err != nil {
		return Workspace{}, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Workspace{}, ErrInvalidWorkspaceID
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Workspace{}, err
	}
	if !info.IsDir() {
		return Workspace{}, ErrInvalidWorkspaceID
	}
	return Workspace{ID: id, Path: resolved}, nil
}
