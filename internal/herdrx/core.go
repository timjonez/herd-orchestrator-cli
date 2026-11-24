package herdrx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// CoreConn is a Client that speaks herdr-core's JSON-RPC 2.0 protocol over its
// local Unix socket.
type CoreConn struct {
	socket string
	seq    atomic.Uint64
	dial   func(ctx context.Context, network, address string) (net.Conn, error)
}

// DialCore returns a herdr-core client for socket. It does not connect until a
// method is called.
func DialCore(socket string) (*CoreConn, error) {
	if socket == "" {
		return nil, fmt.Errorf("%w: empty socket path", ErrUnavailable)
	}
	return &CoreConn{
		socket: socket,
		dial:   (&net.Dialer{}).DialContext,
	}, nil
}

// DialTarget constructs a Client for a resolved Target. When target.Backend is
// BackendAuto (an explicit --socket without --backend), it probes the socket
// with herdr-core's hello handshake and falls back to the Herdr TUI client.
func DialTarget(ctx context.Context, target Target) (Client, error) {
	switch target.Backend {
	case BackendCore:
		return DialCore(target.Socket)
	case BackendTUI:
		return Dial(target.Socket)
	case BackendAuto, "":
		if target.Socket == "" {
			return nil, fmt.Errorf("%w: empty socket path", ErrUnavailable)
		}
		core, err := DialCore(target.Socket)
		if err != nil {
			return nil, err
		}
		probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		if _, err := core.Ping(probeCtx); err == nil {
			return core, nil
		}
		return Dial(target.Socket)
	default:
		return nil, fmt.Errorf("%w: unknown backend %q", ErrInvalid, target.Backend)
	}
}

// Socket returns the configured path.
func (c *CoreConn) Socket() string { return c.socket }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *rpcError) toAPIError() *APIError {
	var data struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(e.Data, &data)
	code := data.Kind
	if code == "" {
		code = fmt.Sprintf("rpc_%d", e.Code)
	}
	return &APIError{Code: code, Message: e.Message}
}

func (c *CoreConn) nextID() uint64 {
	return c.seq.Add(1)
}

func (c *CoreConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	conn, err := c.dial(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnavailable, c.socket, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if params == nil {
		params = map[string]any{}
	}

	id := c.nextID()
	if err := writeJSONLine(conn, rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}); err != nil {
		return nil, err
	}

	raw, err := readLine(ctx, conn, bufio.NewReader(conn))
	if err != nil {
		return nil, err
	}
	var resp rpcResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.JSONRPC != "2.0" {
		return nil, fmt.Errorf("not a herdr-core JSON-RPC 2.0 response")
	}
	if resp.Error != nil {
		return nil, resp.Error.toAPIError()
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("empty result for %s", method)
	}
	return resp.Result, nil
}

type coreStatus struct {
	App   string `json:"app"`
	State string `json:"state"`
	Title string `json:"title"`
	Msg   string `json:"msg"`
	Kind  string `json:"kind"`
}

type coreTerminal struct {
	Address   string            `json:"address"`
	Name      *string           `json:"name"`
	Argv      []string          `json:"argv"`
	Cwd       string            `json:"cwd"`
	State     string            `json:"state"`
	Status    *coreStatus       `json:"status"`
	StatusMS  *uint64           `json:"status_ms"`
	Reported  bool              `json:"reported"`
	Template  *string           `json:"template"`
	Title     string            `json:"title"`
	Turn      uint64            `json:"turn"`
	CreatedMS uint64            `json:"created_ms"`
	Labels    map[string]string `json:"labels"`
}

func (t coreTerminal) isAgent() bool {
	if t.State != "" && t.State != "live" {
		return false
	}
	if t.Status != nil || t.Reported {
		return true
	}
	if t.Template != nil && *t.Template != "" {
		return true
	}
	if t.Labels["herd.managed"] == "1" {
		return true
	}
	return false
}

func (t coreTerminal) toAgent() Agent {
	name := ""
	if t.Name != nil {
		name = *t.Name
	}
	status := ""
	app := ""
	statusTitle := ""
	if t.Status != nil {
		status = t.Status.State
		app = t.Status.App
		if t.Status.Title != "" {
			statusTitle = t.Status.Title
		} else if t.Status.Msg != "" {
			statusTitle = t.Status.Msg
		}
	}
	if app == "" && t.Template != nil && *t.Template != "" {
		app = *t.Template
	}
	if app == "" && t.Labels["herd.kind"] != "" {
		app = t.Labels["herd.kind"]
	}
	if app == "" && len(t.Argv) > 0 && t.Argv[0] != "" {
		app = filepath.Base(t.Argv[0])
	}

	wsID := t.Labels["herdr.space"]
	if wsID == "" {
		wsID = t.Address
	}
	tabID := t.Labels["herdr.tab"]

	var seq uint64
	if t.StatusMS != nil {
		seq = *t.StatusMS
	} else if t.Turn > 0 {
		seq = t.Turn
	} else {
		seq = t.CreatedMS
	}

	displayTitle := statusTitle
	if displayTitle == "" {
		displayTitle = t.Title
	}

	ready := t.State == "live" && (status == "idle" || status == "done")

	return Agent{
		Agent:            app,
		Name:             name,
		DisplayAgent:     app,
		Status:           status,
		PaneID:           t.Address,
		TabID:            tabID,
		WorkspaceID:      wsID,
		TerminalID:       t.Address,
		Title:            t.Title,
		TerminalTitle:    displayTitle,
		Revision:         t.Turn,
		StateChangeSeq:   seq,
		StateLabels:      t.Labels,
		InteractiveReady: ready,
	}
}

func (c *CoreConn) Ping(ctx context.Context) (Pong, error) {
	raw, err := c.call(ctx, "hello", map[string]any{})
	if err != nil {
		return Pong{}, err
	}
	var out struct {
		Version    string `json:"version"`
		Generation uint32 `json:"generation"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Pong{}, err
	}
	return Pong{Version: out.Version, Protocol: out.Generation, Backend: BackendCore}, nil
}

func (c *CoreConn) ListAgents(ctx context.Context) ([]Agent, error) {
	raw, err := c.call(ctx, "terminal.list", map[string]any{"all": false})
	if err != nil {
		return nil, err
	}
	var out struct {
		Terminals []coreTerminal `json:"terminals"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	agents := make([]Agent, 0, len(out.Terminals))
	for _, t := range out.Terminals {
		if !t.isAgent() {
			continue
		}
		agents = append(agents, t.toAgent())
	}
	return agents, nil
}

func (c *CoreConn) ReadAgent(ctx context.Context, target, source string, lines int) (Read, error) {
	if target == "" {
		return Read{}, fmt.Errorf("%w: empty read target", ErrInvalid)
	}
	readLines := lines
	if source == "detection" || readLines < 0 {
		readLines = 0
	}
	raw, err := c.call(ctx, "terminal.read", map[string]any{
		"terminal": target,
		"lines":    readLines,
	})
	if err != nil {
		return Read{}, err
	}
	var out struct {
		Text     string `json:"text"`
		Complete bool   `json:"complete"`
		How      string `json:"how"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Read{}, err
	}
	return Read{
		PaneID:    target,
		Text:      out.Text,
		Source:    out.How,
		Truncated: !out.Complete,
	}, nil
}

func (c *CoreConn) Notify(ctx context.Context, title, body, sound string) (Notification, error) {
	if title == "" {
		return Notification{}, fmt.Errorf("%w: notification title is required", ErrInvalid)
	}
	if runtime.GOOS == "darwin" {
		if bin, err := exec.LookPath("osascript"); err == nil {
			script := fmt.Sprintf("display notification %q with title %q", body, title)
			if err := exec.CommandContext(ctx, bin, "-e", script).Run(); err == nil {
				return Notification{Shown: true, Reason: "osascript"}, nil
			}
		}
	} else {
		if bin, err := exec.LookPath("notify-send"); err == nil {
			args := []string{title}
			if body != "" {
				args = append(args, body)
			}
			if err := exec.CommandContext(ctx, bin, args...).Run(); err == nil {
				return Notification{Shown: true, Reason: "notify-send"}, nil
			}
		}
	}
	return Notification{Shown: false, Reason: "unsupported_in_herdr_core"}, nil
}

// LaunchAgent creates a new herdr-core terminal running in.Kind with in.Args.
func (c *CoreConn) LaunchAgent(ctx context.Context, in AgentLaunch) (Agent, error) {
	if in.Kind == "" {
		return Agent{}, fmt.Errorf("%w: agent kind is required", ErrInvalid)
	}
	argv := make([]string, 0, 1+len(in.Args))
	argv = append(argv, in.Kind)
	argv = append(argv, in.Args...)

	labels := map[string]string{
		"herd.managed": "1",
		"herd.kind":    in.Kind,
	}
	if in.Label != "" {
		labels["herd.label"] = in.Label
	}

	params := map[string]any{
		"argv":           argv,
		"labels":         labels,
		"no_parent":      true,
		"no_notify":      true,
		"outlive_parent": true,
		"wait":           false,
	}
	if in.Cwd != "" {
		params["cwd"] = in.Cwd
	}
	if in.Name != "" {
		params["name"] = in.Name
	}

	raw, err := c.call(ctx, "terminal.run", params)
	if err != nil {
		return Agent{}, err
	}
	var t coreTerminal
	if err := json.Unmarshal(raw, &t); err != nil {
		return Agent{}, err
	}
	ag := t.toAgent()
	if ag.Agent == "" {
		ag.Agent = in.Kind
	}
	if ag.Name == "" {
		ag.Name = in.Name
	}
	return ag, nil
}

func (c *CoreConn) CreateWorkspace(ctx context.Context, in WorkspaceCreate) (WorkspaceCreated, error) {
	return WorkspaceCreated{}, fmt.Errorf("%w: workspace.create is not supported in herdr-core; use LaunchAgent", ErrInvalid)
}

func (c *CoreConn) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	raw, err := c.call(ctx, "terminal.list", map[string]any{"all": false})
	if err != nil {
		return nil, err
	}
	var out struct {
		Terminals []coreTerminal `json:"terminals"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	list := make([]Workspace, 0, len(out.Terminals))
	for i, t := range out.Terminals {
		if t.State != "" && t.State != "live" {
			continue
		}
		label := t.Labels["herd.label"]
		if label == "" && t.Name != nil {
			label = *t.Name
		}
		if label == "" {
			label = t.Labels["where"]
		}
		if label == "" {
			label = t.Address
		}
		status := ""
		if t.Status != nil {
			status = t.Status.State
		}
		list = append(list, Workspace{
			WorkspaceID: t.Address,
			Label:       label,
			Number:      i + 1,
			PaneCount:   1,
			TabCount:    1,
			AgentStatus: status,
		})
	}
	return list, nil
}

func (c *CoreConn) CloseWorkspace(ctx context.Context, workspaceID string) error {
	if workspaceID == "" {
		return fmt.Errorf("%w: empty terminal id", ErrInvalid)
	}
	if _, err := c.call(ctx, "terminal.kill", map[string]any{"terminal": workspaceID}); err != nil {
		return err
	}
	// Best-effort cleanup of the finished terminal record.
	_, _ = c.call(ctx, "terminal.rm", map[string]any{"terminal": workspaceID})
	return nil
}

func (c *CoreConn) StartAgent(ctx context.Context, in AgentStart) (Agent, error) {
	return Agent{}, fmt.Errorf("%w: agent.start is not supported in herdr-core; use LaunchAgent", ErrInvalid)
}

func (c *CoreConn) GetAgent(ctx context.Context, target string) (Agent, error) {
	if target == "" {
		return Agent{}, fmt.Errorf("%w: empty agent target", ErrInvalid)
	}
	raw, err := c.call(ctx, "terminal.get", map[string]any{"terminal": target})
	if err != nil {
		return Agent{}, err
	}
	var t coreTerminal
	if err := json.Unmarshal(raw, &t); err != nil {
		return Agent{}, err
	}
	return t.toAgent(), nil
}

func (c *CoreConn) RenameAgent(ctx context.Context, target, name string) (Agent, error) {
	if target == "" {
		return Agent{}, fmt.Errorf("%w: empty rename target", ErrInvalid)
	}
	if name == "" {
		return Agent{}, fmt.Errorf("%w: empty agent name", ErrInvalid)
	}
	ag, err := c.GetAgent(ctx, target)
	if err != nil {
		return Agent{}, err
	}
	if ag.Name == name {
		return ag, nil
	}
	return Agent{}, fmt.Errorf("%w: renaming terminals is not supported in herdr-core", ErrInvalid)
}

func (c *CoreConn) WaitAgent(ctx context.Context, target string, until []string, timeoutMS int) (Agent, error) {
	if target == "" {
		return Agent{}, fmt.Errorf("%w: empty wait target", ErrInvalid)
	}
	params := map[string]any{"terminal": target}
	if timeoutMS > 0 {
		params["timeout"] = float64(timeoutMS) / 1000.0
	}
	raw, err := c.call(ctx, "terminal.wait", params)
	if err != nil {
		return Agent{}, err
	}
	var out struct {
		Terminal string  `json:"terminal"`
		Ended    bool    `json:"ended"`
		Reason   string  `json:"reason"`
		Status   *string `json:"status"`
		Turn     uint64  `json:"turn"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Agent{}, err
	}
	if ag, err := c.GetAgent(ctx, target); err == nil {
		return ag, nil
	}
	status := ""
	if out.Status != nil {
		status = *out.Status
	}
	return Agent{
		PaneID:           out.Terminal,
		TerminalID:       out.Terminal,
		Status:           status,
		Revision:         out.Turn,
		InteractiveReady: status == "idle" || status == "done",
	}, nil
}

func (c *CoreConn) PromptAgent(ctx context.Context, target, text string) (Agent, error) {
	if target == "" {
		return Agent{}, fmt.Errorf("%w: empty prompt target", ErrInvalid)
	}
	if text == "" {
		return Agent{}, fmt.Errorf("%w: empty prompt text", ErrInvalid)
	}
	_, err := c.call(ctx, "terminal.send", map[string]any{
		"terminal": target,
		"text":     text,
		"wait":     false,
	})
	if err != nil {
		return Agent{}, err
	}
	if ag, err := c.GetAgent(ctx, target); err == nil {
		return ag, nil
	}
	return Agent{PaneID: target, TerminalID: target, Status: "working"}, nil
}

func (c *CoreConn) Subscribe(ctx context.Context, subs []Subscription, handle func(Event) error) error {
	if handle == nil {
		return fmt.Errorf("%w: nil subscribe handler", ErrInvalid)
	}
	conn, err := c.dial(ctx, "unix", c.socket)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnavailable, c.socket, err)
	}
	defer conn.Close()

	id := c.nextID()
	if err := writeJSONLine(conn, rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "events.subscribe",
		Params:  map[string]any{},
	}); err != nil {
		return err
	}

	r := bufio.NewReader(conn)

	// First line is the subscribe acknowledgement with initial snapshot.
	ack, err := readLine(ctx, conn, r)
	if err != nil {
		return err
	}
	var resp rpcResponse
	if err := json.Unmarshal(ack, &resp); err != nil {
		return fmt.Errorf("decode subscribe ack: %w", err)
	}
	if resp.Error != nil {
		return resp.Error.toAPIError()
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := readLine(ctx, conn, r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return err
			}
			return err
		}
		ev, ok := parseCoreEvent(line)
		if !ok {
			continue
		}
		if err := handle(ev); err != nil {
			return err
		}
	}
}

func parseCoreEvent(raw []byte) (Event, bool) {
	var env struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Event{}, false
	}
	switch env.Method {
	case "heartbeat":
		return Event{}, false
	case "missed":
		return Event{Kind: "pane.created", Raw: append(json.RawMessage(nil), raw...)}, true
	case "event":
		var p struct {
			Type     string          `json:"type"`
			Terminal string          `json:"terminal"`
			Data     json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return Event{}, false
		}
		switch strings.ToLower(p.Type) {
		case "status", "turn":
			var d struct {
				Status *coreStatus `json:"status"`
			}
			_ = json.Unmarshal(p.Data, &d)
			ev := Event{
				Kind:   "pane.agent_status_changed",
				PaneID: p.Terminal,
				Raw:    append(json.RawMessage(nil), raw...),
			}
			if d.Status != nil {
				ev.Agent = d.Status.App
				ev.Status = d.Status.State
				ev.Title = d.Status.Title
			}
			return ev, true
		case "created", "restarted":
			return Event{
				Kind:   "pane.created",
				PaneID: p.Terminal,
				Raw:    append(json.RawMessage(nil), raw...),
			}, true
		case "finished", "removed", "lost":
			return Event{
				Kind:   "pane.closed",
				PaneID: p.Terminal,
				Raw:    append(json.RawMessage(nil), raw...),
			}, true
		default:
			return Event{}, false
		}
	default:
		return Event{}, false
	}
}
