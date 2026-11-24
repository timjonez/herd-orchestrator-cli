package herdrx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Supported Herdr runtime backends.
const (
	BackendAuto = "auto"
	BackendTUI  = "tui"
	BackendCore = "core"
)

// Target is a resolved Herdr socket path and backend.
type Target struct {
	Socket  string
	Backend string
}

// ResolveSocket picks the Herdr TUI Unix socket.
//
// Order: explicit socket, HERDR_SOCKET_PATH, named session
// (--session / HERDR_SESSION), then the default session socket.
func ResolveSocket(explicitSocket, session string) string {
	if explicitSocket != "" {
		return explicitSocket
	}
	if env := os.Getenv("HERDR_SOCKET_PATH"); env != "" {
		return env
	}
	if session == "" {
		session = os.Getenv("HERDR_SESSION")
	}
	base := herdrConfigDir()
	if session != "" {
		return filepath.Join(base, "sessions", session, "herdr.sock")
	}
	return filepath.Join(base, "herdr.sock")
}

// ResolveCoreSocket picks the herdr-core Unix socket.
//
// Order: explicit socket, HC_TERMINAL_SOCKET, then the default core socket
// ($XDG_RUNTIME_DIR/herdr-core/default.sock on Linux).
func ResolveCoreSocket(explicitSocket string) string {
	if explicitSocket != "" {
		return explicitSocket
	}
	if env := os.Getenv("HC_TERMINAL_SOCKET"); env != "" {
		return env
	}
	return defaultCoreSocket()
}

// ResolveTarget picks both the socket path and backend (tui, core, or auto for
// an explicit socket that will be probed on connect).
func ResolveTarget(explicitSocket, session, backend string) (Target, error) {
	b := strings.ToLower(strings.TrimSpace(backend))
	if b == "" {
		b = strings.ToLower(strings.TrimSpace(os.Getenv("HERD_BACKEND")))
	}
	if b == "" {
		b = BackendAuto
	}

	switch b {
	case BackendTUI:
		return Target{
			Socket:  ResolveSocket(explicitSocket, session),
			Backend: BackendTUI,
		}, nil
	case BackendCore:
		return Target{
			Socket:  ResolveCoreSocket(explicitSocket),
			Backend: BackendCore,
		}, nil
	case BackendAuto:
		if explicitSocket != "" {
			return Target{
				Socket:  explicitSocket,
				Backend: BackendAuto,
			}, nil
		}
		if env := os.Getenv("HERDR_SOCKET_PATH"); env != "" {
			return Target{Socket: env, Backend: BackendTUI}, nil
		}
		if session != "" || os.Getenv("HERDR_SESSION") != "" {
			return Target{Socket: ResolveSocket("", session), Backend: BackendTUI}, nil
		}
		if env := os.Getenv("HC_TERMINAL_SOCKET"); env != "" {
			return Target{Socket: env, Backend: BackendCore}, nil
		}

		tuiSock := ResolveSocket("", "")
		coreSock := defaultCoreSocket()
		tuiExists := socketExists(tuiSock)
		coreExists := socketExists(coreSock)

		if (os.Getenv("HERDR_PANE_ID") != "" || os.Getenv("HERDR_ENV") != "") && tuiExists {
			return Target{Socket: tuiSock, Backend: BackendTUI}, nil
		}
		if os.Getenv("HC_TERMINAL_ID") != "" && coreExists {
			return Target{Socket: coreSock, Backend: BackendCore}, nil
		}
		if coreExists && !tuiExists {
			return Target{Socket: coreSock, Backend: BackendCore}, nil
		}
		return Target{Socket: tuiSock, Backend: BackendTUI}, nil
	default:
		return Target{}, fmt.Errorf("%w: unknown backend %q (want auto, tui, or core)", ErrInvalid, backend)
	}
}

// SelfTarget returns the current pane/terminal id from the environment so herd
// ignores itself.
func SelfTarget(backend string) string {
	switch backend {
	case BackendCore:
		if v := os.Getenv("HC_TERMINAL_ID"); v != "" {
			return v
		}
		return os.Getenv("HERDR_PANE_ID")
	default:
		if v := os.Getenv("HERDR_PANE_ID"); v != "" {
			return v
		}
		return os.Getenv("HC_TERMINAL_ID")
	}
}

// SessionKey is the durable queue partition for a Herdr session.
func SessionKey(session string) string {
	if session != "" {
		return sanitizeSessionKey(session)
	}
	if env := os.Getenv("HERDR_SESSION"); env != "" {
		return sanitizeSessionKey(env)
	}
	return "default"
}

func herdrConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "herdr")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".config", "herdr")
	}
	return filepath.Join(home, ".config", "herdr")
}

func defaultCoreSocket() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "herdr-core", "default.sock")
	}
	if runtime.GOOS == "linux" {
		return filepath.Join(fmt.Sprintf("/run/user/%d", os.Getuid()), "herdr-core", "default.sock")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, "Library", "Application Support", "herdr-core", "default.sock")
		}
		return filepath.Join(home, ".local", "state", "herdr-core", "default.sock")
	}
	return filepath.Join(os.TempDir(), "herdr-core", "default.sock")
}

func socketExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func sanitizeSessionKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		return "default"
	}
	return out
}
