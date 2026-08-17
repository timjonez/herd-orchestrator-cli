package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timjonez/herd-orchestrator-cli/internal/herdrx"
	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
)

type fakeClient struct {
	agents  []herdrx.Agent
	pong    herdrx.Pong
	notices []string
}

func (f *fakeClient) Socket() string { return "/tmp/fake.sock" }
func (f *fakeClient) Ping(ctx context.Context) (herdrx.Pong, error) {
	return f.pong, nil
}
func (f *fakeClient) ListAgents(ctx context.Context) ([]herdrx.Agent, error) {
	return f.agents, nil
}
func (f *fakeClient) ReadAgent(ctx context.Context, target, source string, lines int) (herdrx.Read, error) {
	return herdrx.Read{Text: "Allow edit?"}, nil
}
func (f *fakeClient) Notify(ctx context.Context, title, body, sound string) (herdrx.Notification, error) {
	f.notices = append(f.notices, title)
	return herdrx.Notification{Shown: true, Reason: "shown"}, nil
}
func (f *fakeClient) Subscribe(ctx context.Context, subs []herdrx.Subscription, handle func(herdrx.Event) error) error {
	<-ctx.Done()
	return ctx.Err()
}

func newTestApp(t *testing.T, fc *fakeClient) (*App, string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	var out, errb bytes.Buffer
	app := NewApp()
	app.Stdout = &out
	app.Stderr = &errb
	app.now = func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }
	if fc == nil {
		fc = &fakeClient{pong: herdrx.Pong{Version: "0.8.0", Protocol: 19}}
	}
	app.newClient = func(socket string) (herdrx.Client, error) { return fc, nil }
	return app, dir, &out, &errb
}

func (a *App) run(stateDir string, args ...string) int {
	full := append([]string{"--state-dir", stateDir}, args...)
	return a.Execute(full)
}

func TestVersionAndQueueCommands(t *testing.T) {
	app, dir, out, errb := newTestApp(t, nil)

	if code := app.run(dir, "version"); code != 0 {
		t.Fatalf("version: %s", errb.String())
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("empty version")
	}

	st, err := queue.Open(queue.DirFor(dir, "default"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = st.Upsert(queue.Draft{
		PaneID:         "w1:p2",
		Name:           "reviewer",
		Agent:          "grok",
		HerdrStatus:    "blocked",
		Kind:           queue.KindNeedsDecision,
		StateChangeSeq: 3,
		Title:          "Allow edit?",
		Excerpt:        "Allow edit to file?",
	}, app.now())
	if err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	if code := app.run(dir, "--json", "pending"); code != 0 {
		t.Fatalf("pending: %s", errb.String())
	}
	var items []queue.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("pending json: %s", out.String())
	}

	out.Reset()
	if code := app.run(dir, "show", "1"); code != 0 {
		t.Fatalf("show: %s", errb.String())
	}
	if !strings.Contains(out.String(), "needs_decision") || !strings.Contains(out.String(), "reviewer") {
		t.Fatalf("show: %s", out.String())
	}

	out.Reset()
	if code := app.run(dir, "ack", "1"); code != 0 {
		t.Fatalf("ack: %s", errb.String())
	}
	out.Reset()
	if code := app.run(dir, "pending"); code != 0 {
		t.Fatalf("pending after ack: %s", errb.String())
	}
	if !strings.Contains(out.String(), "(no items)") {
		t.Fatalf("expected empty pending, got %q", out.String())
	}
}

func TestStatusJSON(t *testing.T) {
	fc := &fakeClient{
		pong: herdrx.Pong{Version: "0.8.0", Protocol: 19},
		agents: []herdrx.Agent{
			{Agent: "grok", Name: "self", Status: "working", PaneID: "w1:p1"},
		},
	}
	app, dir, out, errb := newTestApp(t, fc)
	app.selfPane = "w1:p1"
	if code := app.run(dir, "--json", "status"); code != 0 {
		t.Fatalf("status: %s", errb.String())
	}
	var s statusJSON
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.HerdrVersion != "0.8.0" || s.Protocol != 19 || len(s.Agents) != 1 || !s.Agents[0].Ignored {
		t.Fatalf("status: %+v", s)
	}
}

func TestNotify(t *testing.T) {
	fc := &fakeClient{pong: herdrx.Pong{Version: "0.8.0", Protocol: 19}}
	app, dir, out, errb := newTestApp(t, fc)
	if code := app.run(dir, "notify", "--title", "hello", "--body", "world"); code != 0 {
		t.Fatalf("notify: %s", errb.String())
	}
	if len(fc.notices) != 1 || fc.notices[0] != "hello" {
		t.Fatalf("notices: %v out=%s", fc.notices, out.String())
	}
}

func TestShowMissing(t *testing.T) {
	app, dir, _, errb := newTestApp(t, nil)
	if code := app.run(dir, "--json", "show", "9"); code != 1 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "not_found") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestQueuePathUsesSession(t *testing.T) {
	app, dir, _, _ := newTestApp(t, nil)
	if code := app.run(dir, "--session", "work", "pending"); code != 0 {
		t.Fatal("pending")
	}
	got := queue.DirFor(dir, "work")
	if filepath.Base(filepath.Dir(got)) != "work" {
		t.Fatalf("path: %s", got)
	}
}
