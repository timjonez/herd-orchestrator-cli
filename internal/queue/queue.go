package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Kind values written onto items.
const (
	KindNeedsDecision = "needs_decision"
	KindSettled       = "settled"
	KindUnknownIdle   = "unknown_idle"
)

// Item is one durable attention event.
type Item struct {
	ID             int        `json:"id"`
	PaneID         string     `json:"pane_id"`
	TabID          string     `json:"tab_id"`
	WorkspaceID    string     `json:"workspace_id"`
	Agent          string     `json:"agent"`
	Name           string     `json:"name"`
	HerdrStatus    string     `json:"herdr_status"`
	Kind           string     `json:"kind"`
	StateChangeSeq uint64     `json:"state_change_seq"`
	Revision       uint64     `json:"revision"`
	Title          string     `json:"title"`
	Excerpt        string     `json:"excerpt"`
	CreatedAt      time.Time  `json:"created_at"`
	NotifiedAt     *time.Time `json:"notified_at,omitempty"`
	AckedAt        *time.Time `json:"acked_at,omitempty"`
	DismissedAt    *time.Time `json:"dismissed_at,omitempty"`
}

// Open reports whether the item still needs attention.
func (it Item) Open() bool {
	return it.AckedAt == nil && it.DismissedAt == nil
}

// Label is the best display name.
func (it Item) Label() string {
	if it.Name != "" {
		return it.Name
	}
	if it.Agent != "" {
		return it.Agent
	}
	return it.PaneID
}

// Draft is the classification of a live agent used to upsert the queue.
type Draft struct {
	PaneID         string
	TabID          string
	WorkspaceID    string
	Agent          string
	Name           string
	HerdrStatus    string
	Kind           string
	StateChangeSeq uint64
	Revision       uint64
	Title          string
	Excerpt        string
}

// UpsertResult describes what Upsert did.
type UpsertResult struct {
	Created      bool
	Updated      bool
	Skipped      bool
	ShouldNotify bool
}

type fileData struct {
	NextID int    `json:"next_id"`
	Items  []Item `json:"items"`
}

// Store is a JSON file of queue items, flocked for concurrent readers/writers.
type Store struct {
	path string
	mu   sync.Mutex
}

// Open a store at path. Parent dirs are created on first write.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty queue path", ErrInvalid)
	}
	return &Store{path: path}, nil
}

// Path returns the queue file path.
func (s *Store) Path() string { return s.path }

// DirFor returns <stateDir>/<sessionKey>/queue.json.
func DirFor(stateDir, sessionKey string) string {
	if sessionKey == "" {
		sessionKey = "default"
	}
	return filepath.Join(stateDir, sessionKey, "queue.json")
}

// DefaultStateDir is $XDG_DATA_HOME/herd or ~/.local/share/herd.
func DefaultStateDir() string {
	if env := os.Getenv("HERD_STATE_DIR"); env != "" {
		return env
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "herd")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".local", "share", "herd")
	}
	return filepath.Join(home, ".local", "share", "herd")
}

// List returns items. When all is false, only open items are returned.
func (s *Store) List(all bool) ([]Item, error) {
	var out []Item
	err := s.withFile(false, func(data *fileData) error {
		for _, it := range data.Items {
			if all || it.Open() {
				out = append(out, it)
			}
		}
		return nil
	})
	if out == nil {
		out = []Item{}
	}
	return out, err
}

// Get returns the item with id.
func (s *Store) Get(id int) (Item, error) {
	var found Item
	err := s.withFile(false, func(data *fileData) error {
		for _, it := range data.Items {
			if it.ID == id {
				found = it
				return nil
			}
		}
		return fmt.Errorf("%w: item %d", ErrNotFound, id)
	})
	return found, err
}

// Upsert records a draft. Same pane + same seq is a no-op. Same pane + new
// seq updates the open item. A closed item with a new seq becomes a new row.
func (s *Store) Upsert(d Draft, now time.Time) (Item, UpsertResult, error) {
	if d.PaneID == "" {
		return Item{}, UpsertResult{}, fmt.Errorf("%w: pane_id is required", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var (
		item Item
		res  UpsertResult
	)
	err := s.withFile(true, func(data *fileData) error {
		if data.NextID < 1 {
			data.NextID = 1
		}
		for i := range data.Items {
			it := &data.Items[i]
			if it.PaneID != d.PaneID || !it.Open() {
				continue
			}
			if it.StateChangeSeq == d.StateChangeSeq {
				res.Skipped = true
				item = *it
				return nil
			}
			applyDraft(it, d)
			res.Updated = true
			res.ShouldNotify = shouldNotify(it.Kind)
			if res.ShouldNotify {
				it.NotifiedAt = nil
			}
			item = *it
			return nil
		}
		if data.NextID < 1 {
			data.NextID = 1
		}
		it := Item{
			ID:        data.NextID,
			CreatedAt: now.UTC(),
		}
		data.NextID++
		applyDraft(&it, d)
		data.Items = append(data.Items, it)
		res.Created = true
		res.ShouldNotify = shouldNotify(it.Kind)
		item = it
		return nil
	})
	return item, res, err
}

func applyDraft(it *Item, d Draft) {
	it.PaneID = d.PaneID
	it.TabID = d.TabID
	it.WorkspaceID = d.WorkspaceID
	it.Agent = d.Agent
	it.Name = d.Name
	it.HerdrStatus = d.HerdrStatus
	it.Kind = d.Kind
	it.StateChangeSeq = d.StateChangeSeq
	it.Revision = d.Revision
	it.Title = d.Title
	it.Excerpt = d.Excerpt
}

func shouldNotify(kind string) bool {
	return kind == KindNeedsDecision
}

// MarkNotified sets notified_at when empty.
func (s *Store) MarkNotified(id int, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return s.withFile(true, func(data *fileData) error {
		for i := range data.Items {
			if data.Items[i].ID != id {
				continue
			}
			t := now.UTC()
			data.Items[i].NotifiedAt = &t
			return nil
		}
		return fmt.Errorf("%w: item %d", ErrNotFound, id)
	})
}

// Ack marks an open item acknowledged.
func (s *Store) Ack(id int, now time.Time) (Item, error) {
	return s.close(id, now, true)
}

// Dismiss marks an open item dismissed.
func (s *Store) Dismiss(id int, now time.Time) (Item, error) {
	return s.close(id, now, false)
}

func (s *Store) close(id int, now time.Time, ack bool) (Item, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var found Item
	err := s.withFile(true, func(data *fileData) error {
		for i := range data.Items {
			if data.Items[i].ID != id {
				continue
			}
			if !data.Items[i].Open() {
				return fmt.Errorf("%w: item %d is already closed", ErrConflict, id)
			}
			t := now.UTC()
			if ack {
				data.Items[i].AckedAt = &t
			} else {
				data.Items[i].DismissedAt = &t
			}
			found = data.Items[i]
			return nil
		}
		return fmt.Errorf("%w: item %d", ErrNotFound, id)
	})
	return found, err
}

func (s *Store) withFile(write bool, fn func(*fileData) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}

	flag := os.O_RDWR | os.O_CREATE
	f, err := os.OpenFile(s.path, flag, 0o600)
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
		return fileData{NextID: 1, Items: []Item{}}, nil
	}
	var data fileData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return fileData{}, fmt.Errorf("decode queue: %w", err)
	}
	if data.Items == nil {
		data.Items = []Item{}
	}
	return data, nil
}

// ParseID converts a CLI id argument.
func ParseID(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: item id %q", ErrInvalid, s)
	}
	return n, nil
}

var (
	// ErrNotFound is an unknown item id.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is bad input.
	ErrInvalid = errors.New("invalid")
	// ErrConflict is a closed item being closed again.
	ErrConflict = errors.New("conflict")
)
