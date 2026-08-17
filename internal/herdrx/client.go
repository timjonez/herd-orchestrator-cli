package herdrx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

// Client talks to a Herdr server over its local socket.
type Client interface {
	Ping(ctx context.Context) (Pong, error)
	ListAgents(ctx context.Context) ([]Agent, error)
	ReadAgent(ctx context.Context, target, source string, lines int) (Read, error)
	Notify(ctx context.Context, title, body, sound string) (Notification, error)
	Subscribe(ctx context.Context, subs []Subscription, handle func(Event) error) error
	Socket() string
}

// Conn is a Client that dials a Unix socket per request.
type Conn struct {
	socket string
	seq    atomic.Uint64
	dial   func(ctx context.Context, network, address string) (net.Conn, error)
}

// Dial returns a client for socket. It does not connect until a method is called.
func Dial(socket string) (*Conn, error) {
	if socket == "" {
		return nil, fmt.Errorf("%w: empty socket path", ErrUnavailable)
	}
	return &Conn{
		socket: socket,
		dial:   (&net.Dialer{}).DialContext,
	}, nil
}

// Socket returns the configured path.
func (c *Conn) Socket() string { return c.socket }

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type wireResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *wireError      `json:"error"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c *Conn) nextID() string {
	n := c.seq.Add(1)
	return fmt.Sprintf("herd-%d", n)
}

func (c *Conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	conn, err := c.dial(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnavailable, c.socket, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	id := c.nextID()
	if err := writeJSONLine(conn, request{ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}

	raw, err := readLine(ctx, conn, bufio.NewReader(conn))
	if err != nil {
		return nil, err
	}
	var resp wireResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, &APIError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("empty result for %s", method)
	}
	return resp.Result, nil
}

func (c *Conn) Ping(ctx context.Context) (Pong, error) {
	raw, err := c.call(ctx, "ping", map[string]any{})
	if err != nil {
		return Pong{}, err
	}
	var out struct {
		Type     string `json:"type"`
		Version  string `json:"version"`
		Protocol uint32 `json:"protocol"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Pong{}, err
	}
	return Pong{Version: out.Version, Protocol: out.Protocol}, nil
}

func (c *Conn) ListAgents(ctx context.Context) ([]Agent, error) {
	raw, err := c.call(ctx, "agent.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Type   string  `json:"type"`
		Agents []Agent `json:"agents"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.Agents == nil {
		out.Agents = []Agent{}
	}
	return out.Agents, nil
}

func (c *Conn) ReadAgent(ctx context.Context, target, source string, lines int) (Read, error) {
	if target == "" {
		return Read{}, fmt.Errorf("%w: empty read target", ErrInvalid)
	}
	if source == "" {
		source = "recent_unwrapped"
	}
	params := map[string]any{
		"target":     target,
		"source":     source,
		"strip_ansi": true,
	}
	if lines > 0 {
		params["lines"] = lines
	}
	raw, err := c.call(ctx, "agent.read", params)
	if err != nil {
		return Read{}, err
	}
	return decodeRead(raw)
}

func decodeRead(raw json.RawMessage) (Read, error) {
	var wrapped struct {
		Type string `json:"type"`
		Read Read   `json:"read"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return Read{}, err
	}
	if wrapped.Read.Text != "" || wrapped.Read.PaneID != "" {
		return wrapped.Read, nil
	}
	if wrapped.Text != "" {
		return Read{Text: wrapped.Text}, nil
	}
	var direct Read
	if err := json.Unmarshal(raw, &direct); err != nil {
		return Read{}, err
	}
	return direct, nil
}

func (c *Conn) Notify(ctx context.Context, title, body, sound string) (Notification, error) {
	if title == "" {
		return Notification{}, fmt.Errorf("%w: notification title is required", ErrInvalid)
	}
	params := map[string]any{"title": title}
	if body != "" {
		params["body"] = body
	}
	if sound != "" {
		params["sound"] = sound
	}
	raw, err := c.call(ctx, "notification.show", params)
	if err != nil {
		return Notification{}, err
	}
	var out Notification
	if err := json.Unmarshal(raw, &out); err != nil {
		return Notification{}, err
	}
	return out, nil
}

func (c *Conn) Subscribe(ctx context.Context, subs []Subscription, handle func(Event) error) error {
	if handle == nil {
		return fmt.Errorf("%w: nil subscribe handler", ErrInvalid)
	}
	if len(subs) == 0 {
		return fmt.Errorf("%w: no subscriptions", ErrInvalid)
	}
	conn, err := c.dial(ctx, "unix", c.socket)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnavailable, c.socket, err)
	}
	defer conn.Close()

	id := c.nextID()
	if err := writeJSONLine(conn, request{
		ID:     id,
		Method: "events.subscribe",
		Params: map[string]any{"subscriptions": subs},
	}); err != nil {
		return err
	}

	r := bufio.NewReader(conn)

	// First line is the subscribe acknowledgement.
	ack, err := readLine(ctx, conn, r)
	if err != nil {
		return err
	}
	var resp wireResponse
	if err := json.Unmarshal(ack, &resp); err != nil {
		return fmt.Errorf("decode subscribe ack: %w", err)
	}
	if resp.Error != nil {
		return &APIError{Code: resp.Error.Code, Message: resp.Error.Message}
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
		ev, err := parseEvent(line)
		if err != nil {
			// Ignore non-event frames (late acks, pings).
			continue
		}
		if err := handle(ev); err != nil {
			return err
		}
	}
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func readLine(ctx context.Context, conn net.Conn, r *bufio.Reader) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetReadDeadline(deadline)
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		line, err := r.ReadBytes('\n')
		ch <- result{b: line, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = conn.SetDeadline(time.Now())
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			if errors.Is(res.err, io.EOF) && len(res.b) == 0 {
				return nil, io.EOF
			}
			if len(res.b) == 0 {
				return nil, res.err
			}
		}
		line := trimNL(res.b)
		if len(line) == 0 {
			if res.err != nil {
				return nil, res.err
			}
			return nil, io.EOF
		}
		return line, nil
	}
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func parseEvent(raw []byte) (Event, error) {
	var env struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
		Type  string          `json:"type"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Event{}, err
	}
	kind := env.Event
	if kind == "" {
		kind = env.Type
	}
	if kind == "" {
		return Event{}, errors.New("not an event")
	}
	ev := Event{Kind: kind, Raw: append(json.RawMessage(nil), raw...)}
	payload := env.Data
	if len(payload) == 0 {
		payload = raw
	}
	var body struct {
		Type        string `json:"type"`
		PaneID      string `json:"pane_id"`
		WorkspaceID string `json:"workspace_id"`
		Agent       string `json:"agent"`
		Status      string `json:"agent_status"`
		Title       string `json:"title"`
		Pane        *struct {
			PaneID      string `json:"pane_id"`
			WorkspaceID string `json:"workspace_id"`
			Agent       string `json:"agent"`
			Status      string `json:"agent_status"`
		} `json:"pane"`
	}
	_ = json.Unmarshal(payload, &body)
	ev.PaneID = body.PaneID
	ev.WorkspaceID = body.WorkspaceID
	ev.Agent = body.Agent
	ev.Status = body.Status
	ev.Title = body.Title
	if ev.PaneID == "" && body.Pane != nil {
		ev.PaneID = body.Pane.PaneID
		if ev.WorkspaceID == "" {
			ev.WorkspaceID = body.Pane.WorkspaceID
		}
		if ev.Agent == "" {
			ev.Agent = body.Pane.Agent
		}
		if ev.Status == "" {
			ev.Status = body.Pane.Status
		}
	}
	return ev, nil
}
