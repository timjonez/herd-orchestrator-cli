package herdrx

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveTarget(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HC_TERMINAL_SOCKET", "")
	t.Setenv("HC_TERMINAL_ID", "")
	t.Setenv("HERD_BACKEND", "")

	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "cfg")
	runDir := filepath.Join(tmp, "run")
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_RUNTIME_DIR", runDir)

	// Explicit backend core uses default core socket.
	got, err := ResolveTarget("", "", BackendCore)
	if err != nil {
		t.Fatal(err)
	}
	wantCore := filepath.Join(runDir, "herdr-core", "default.sock")
	if got.Backend != BackendCore || got.Socket != wantCore {
		t.Fatalf("explicit core: %+v", got)
	}

	// HC_TERMINAL_SOCKET selects core in auto mode.
	t.Setenv("HC_TERMINAL_SOCKET", "/tmp/custom-hc.sock")
	got, err = ResolveTarget("", "", BackendAuto)
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != BackendCore || got.Socket != "/tmp/custom-hc.sock" {
		t.Fatalf("HC_TERMINAL_SOCKET: %+v", got)
	}
	t.Setenv("HC_TERMINAL_SOCKET", "")

	// When only the core socket exists on disk, auto mode picks core.
	if err := os.MkdirAll(filepath.Dir(wantCore), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantCore, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveTarget("", "", BackendAuto)
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != BackendCore || got.Socket != wantCore {
		t.Fatalf("disk core auto: %+v", got)
	}

	// When both sockets exist and HC_TERMINAL_ID is set, auto mode picks core.
	wantTUI := filepath.Join(cfgDir, "herdr", "herdr.sock")
	if err := os.MkdirAll(filepath.Dir(wantTUI), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantTUI, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_TERMINAL_ID", "t7")
	got, err = ResolveTarget("", "", BackendAuto)
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != BackendCore || got.Socket != wantCore {
		t.Fatalf("both exist with HC_TERMINAL_ID: %+v", got)
	}

	// Explicit --socket in auto mode defers backend detection to DialTarget.
	got, err = ResolveTarget("/custom.sock", "", BackendAuto)
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != BackendAuto || got.Socket != "/custom.sock" {
		t.Fatalf("explicit socket auto: %+v", got)
	}

	// Invalid backend returns ErrInvalid.
	if _, err := ResolveTarget("", "", "bogus"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestCoreCallAndSubscribe(t *testing.T) {
	var methods []string
	var lastParams map[string]any
	sock := startServer(t, func(req map[string]any, w *bufio.Writer) {
		method, _ := req["method"].(string)
		id := req["id"]
		methods = append(methods, method)
		if p, ok := req["params"].(map[string]any); ok {
			lastParams = p
		}
		switch method {
		case "hello":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"version":    "0.1.21",
					"generation": 1,
				},
			})
		case "terminal.list":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"hidden_finished": 0,
					"machines":        []any{},
					"terminals": []map[string]any{
						{
							"address":   "t1",
							"argv":      []string{"/bin/bash"},
							"cwd":       "/tmp",
							"state":     "live",
							"reported":  false,
							"status":    nil,
							"labels":    map[string]string{},
							"title":     "bash",
							"turn":      0,
							"created_ms": 100,
						},
						{
							"address":   "t2",
							"name":      "reviewer",
							"argv":      []string{"claude"},
							"cwd":       "/tmp/repo",
							"state":     "live",
							"reported":  true,
							"status":    map[string]any{"app": "claude", "state": "blocked", "title": "Allow edit?"},
							"status_ms": 1700000000000,
							"labels":    map[string]string{"herdr.space": "sp1", "herdr.tab": "tb1"},
							"title":     "bash",
							"turn":      5,
							"created_ms": 100,
						},
					},
				},
			})
		case "terminal.read":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"text":     "Allow edit to file?\n[y/n]",
					"complete": true,
					"how":      "screen",
				},
			})
		case "terminal.run":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"address":    "t9",
					"name":       "fix-login",
					"argv":       []string{"claude", "--permission-mode", "auto"},
					"cwd":        "/tmp/proj",
					"state":      "live",
					"reported":   false,
					"status":     nil,
					"labels":     map[string]string{"herd.managed": "1", "herd.kind": "claude"},
					"title":      "",
					"turn":       0,
					"created_ms": 200,
				},
			})
		case "terminal.wait":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"terminal": "t9",
					"ended":    true,
					"reason":   "status",
					"status":   "idle",
					"turn":     1,
					"children": []any{},
				},
			})
		case "terminal.get":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"address":    "t9",
					"name":       "fix-login",
					"argv":       []string{"claude"},
					"cwd":        "/tmp/proj",
					"state":      "live",
					"reported":   true,
					"status":     map[string]any{"app": "claude", "state": "idle"},
					"status_ms":  300,
					"labels":     map[string]string{"herd.managed": "1"},
					"title":      "claude",
					"turn":       1,
					"created_ms": 200,
				},
			})
		case "terminal.send":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"started": true,
					"status":  "idle",
					"turn":    1,
				},
			})
		case "terminal.kill":
			if lastParams["terminal"] == "missing" {
				writeLine(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"error": map[string]any{
						"code":    -32000,
						"message": "not_found",
						"data":    map[string]any{"kind": "not_found"},
					},
				})
				return
			}
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"address": "t9", "state": "finished"},
			})
		case "terminal.rm":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"removed": true},
			})
		case "events.subscribe":
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"seq": 10, "terminals": []any{}},
			})
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"method":  "heartbeat",
				"params":  map[string]any{"seq": 10},
			})
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"method":  "event",
				"params": map[string]any{
					"type":     "status",
					"terminal": "t2",
					"seq":      11,
					"data": map[string]any{
						"status": map[string]any{"app": "claude", "state": "blocked", "title": "Allow edit?"},
					},
				},
			})
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"method":  "event",
				"params": map[string]any{
					"type":     "created",
					"terminal": "t3",
					"seq":      12,
					"data":     map[string]any{"address": "t3"},
				},
			})
		default:
			writeLine(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]any{
					"code":    -32601,
					"message": "method not found",
				},
			})
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// DialTarget with BackendAuto should probe hello and return *CoreConn.
	c, err := DialTarget(ctx, Target{Socket: sock, Backend: BackendAuto})
	if err != nil {
		t.Fatal(err)
	}
	core, ok := c.(*CoreConn)
	if !ok {
		t.Fatalf("expected *CoreConn from auto probe, got %T", c)
	}

	pong, err := core.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pong.Version != "0.1.21" || pong.Protocol != 1 || pong.Backend != BackendCore {
		t.Fatalf("pong: %+v", pong)
	}

	agents, err := core.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Plain shell t1 should be filtered out; only agent t2 returned.
	if len(agents) != 1 || agents[0].PaneID != "t2" || agents[0].Name != "reviewer" || agents[0].Status != "blocked" || agents[0].WorkspaceID != "sp1" || agents[0].StateChangeSeq != 1700000000000 {
		t.Fatalf("agents: %+v", agents)
	}

	rd, err := core.ReadAgent(ctx, "t2", "detection", 0)
	if err != nil || rd.Text != "Allow edit to file?\n[y/n]" {
		t.Fatalf("read: %+v %v", rd, err)
	}

	launched, err := core.LaunchAgent(ctx, AgentLaunch{
		Cwd: "/tmp/proj", Label: "fix-login", Name: "fix-login", Kind: "claude",
		Args: []string{"--permission-mode", "auto"},
	})
	if err != nil || launched.PaneID != "t9" || launched.WorkspaceID != "t9" || launched.Name != "fix-login" {
		t.Fatalf("launch: %+v %v", launched, err)
	}

	waited, err := core.WaitAgent(ctx, "fix-login", []string{"idle", "done", "blocked"}, 5000)
	if err != nil || waited.Status != "idle" || !waited.InteractiveReady {
		t.Fatalf("wait: %+v %v", waited, err)
	}

	if _, err := core.PromptAgent(ctx, "fix-login", "fix it"); err != nil {
		t.Fatal(err)
	}

	if err := core.CloseWorkspace(ctx, "t9"); err != nil {
		t.Fatal(err)
	}
	if err := core.CloseWorkspace(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for missing, got %v", err)
	}

	var events []Event
	subCtx, subCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer subCancel()
	err = core.Subscribe(subCtx, nil, func(ev Event) error {
		events = append(events, ev)
		if len(events) == 2 {
			subCancel()
		}
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events: %+v", events)
	}
	if !events[0].StatusChanged() || events[0].PaneID != "t2" || events[0].Status != "blocked" {
		t.Fatalf("status event: %+v", events[0])
	}
	if !events[1].NeedsReconcile() || events[1].PaneID != "t3" {
		t.Fatalf("created event: %+v", events[1])
	}
}
