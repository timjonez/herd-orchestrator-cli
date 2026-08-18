package spaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Space is one workspace created by herd new.
type Space struct {
	WorkspaceID string    `json:"workspace_id"`
	TabID       string    `json:"tab_id"`
	PaneID      string    `json:"pane_id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	Cwd         string    `json:"cwd"`
	Auto        bool      `json:"auto"`
	Prompt      string    `json:"prompt,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type fileData struct {
	Spaces []Space `json:"spaces"`
}

// Store is a JSON file of herd-created workspaces, flocked for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
}

// Open a store at path. Parent dirs are created on first write.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty spaces path", ErrInvalid)
	}
	return &Store{path: path}, nil
}

// Path returns the spaces file path.
func (s *Store) Path() string { return s.path }

// DirFor returns <stateDir>/<sessionKey>/spaces.json.
func DirFor(stateDir, sessionKey string) string {
	if sessionKey == "" {
		sessionKey = "default"
	}
	return filepath.Join(stateDir, sessionKey, "spaces.json")
}

// List returns recorded spaces.
func (s *Store) List() ([]Space, error) {
	var out []Space
	err := s.withFile(false, func(data *fileData) error {
		out = append(out, data.Spaces...)
		return nil
	})
	if out == nil {
		out = []Space{}
	}
	return out, err
}

// Get finds a space by workspace id or unique agent name.
func (s *Store) Get(id string) (Space, error) {
	if id == "" {
		return Space{}, fmt.Errorf("%w: empty space id", ErrInvalid)
	}
	var found []Space
	err := s.withFile(false, func(data *fileData) error {
		for _, sp := range data.Spaces {
			if sp.WorkspaceID == id {
				found = []Space{sp}
				return nil
			}
			if sp.Name == id {
				found = append(found, sp)
			}
		}
		return nil
	})
	if err != nil {
		return Space{}, err
	}
	switch len(found) {
	case 0:
		return Space{}, fmt.Errorf("%w: space %q", ErrNotFound, id)
	case 1:
		return found[0], nil
	default:
		return Space{}, fmt.Errorf("%w: name %q matches %d workspaces", ErrConflict, id, len(found))
	}
}

// Put records a space. The same workspace id replaces the previous row.
func (s *Store) Put(sp Space, now time.Time) (Space, error) {
	if sp.WorkspaceID == "" {
		return Space{}, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if sp.CreatedAt.IsZero() {
		sp.CreatedAt = now.UTC()
	} else {
		sp.CreatedAt = sp.CreatedAt.UTC()
	}
	err := s.withFile(true, func(data *fileData) error {
		for i := range data.Spaces {
			if data.Spaces[i].WorkspaceID == sp.WorkspaceID {
				data.Spaces[i] = sp
				return nil
			}
		}
		data.Spaces = append(data.Spaces, sp)
		return nil
	})
	return sp, err
}

// Remove deletes the space with workspace id.
func (s *Store) Remove(workspaceID string) (Space, error) {
	if workspaceID == "" {
		return Space{}, fmt.Errorf("%w: empty workspace id", ErrInvalid)
	}
	var found Space
	err := s.withFile(true, func(data *fileData) error {
		for i, sp := range data.Spaces {
			if sp.WorkspaceID != workspaceID {
				continue
			}
			found = sp
			data.Spaces = append(data.Spaces[:i], data.Spaces[i+1:]...)
			return nil
		}
		return fmt.Errorf("%w: space %q", ErrNotFound, workspaceID)
	})
	return found, err
}

func (s *Store) withFile(write bool, fn func(*fileData) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}

	f, err := os.OpenFile(s.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	lock := syscall.LOCK_SH
	if write {
		lock = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), lock); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	data, err := decodeFile(f)
	if err != nil {
		return err
	}
	if err := fn(&data); err != nil {
		return err
	}
	if !write {
		return nil
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(data); err != nil {
		return err
	}
	return f.Sync()
}

func decodeFile(f *os.File) (fileData, error) {
	stat, err := f.Stat()
	if err != nil {
		return fileData{}, err
	}
	if stat.Size() == 0 {
		return fileData{Spaces: []Space{}}, nil
	}
	var data fileData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return fileData{}, fmt.Errorf("decode spaces: %w", err)
	}
	if data.Spaces == nil {
		data.Spaces = []Space{}
	}
	return data, nil
}

var (
	// ErrNotFound is an unknown workspace or name.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is bad input.
	ErrInvalid = errors.New("invalid")
	// ErrConflict is an ambiguous name or a blocked teardown.
	ErrConflict = errors.New("conflict")
)
