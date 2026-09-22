package spaces

import (
	"errors"
	"time"
)

// Space is the result of herd new.
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

var (
	// ErrNotFound is an unknown workspace or name.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is bad input.
	ErrInvalid = errors.New("invalid")
	// ErrConflict is an ambiguous name or a blocked teardown.
	ErrConflict = errors.New("conflict")
)
