package cli

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/timjonez/herd-orchestrator-cli/internal/herdrx"
	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
	"github.com/timjonez/herd-orchestrator-cli/internal/spaces"
)

type jsonError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func (a *App) writeJSON(v any) error {
	return a.writeJSONTo(a.Stdout, v)
}

func (a *App) writeJSONError(err error) {
	_ = a.writeJSONTo(a.Stderr, jsonError{
		Error: err.Error(),
		Code:  errorCode(err),
	})
}

func (a *App) writeJSONTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, queue.ErrNotFound), errors.Is(err, herdrx.ErrNotFound), errors.Is(err, spaces.ErrNotFound):
		return "not_found"
	case errors.Is(err, queue.ErrConflict), errors.Is(err, spaces.ErrConflict):
		return "conflict"
	case errors.Is(err, queue.ErrInvalid), errors.Is(err, herdrx.ErrInvalid), errors.Is(err, spaces.ErrInvalid):
		return "invalid"
	case errors.Is(err, herdrx.ErrUnavailable):
		return "unavailable"
	default:
		var api *herdrx.APIError
		if errors.As(err, &api) && api.Code != "" {
			return api.Code
		}
		return "error"
	}
}

func (a *App) emit(jsonVal any, quietOK bool, human func()) error {
	if a.JSON {
		return a.writeJSON(jsonVal)
	}
	if quietOK && a.Quiet {
		return nil
	}
	human()
	return nil
}

func (a *App) emitAlways(jsonVal any, human func()) error {
	if a.JSON {
		return a.writeJSON(jsonVal)
	}
	human()
	return nil
}
