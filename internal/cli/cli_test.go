package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timjonez/herd-orchestrator-cli/internal/herdrx"
	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
	"github.com/timjonez/herd-orchestrator-cli/internal/spaces"
)

type fakeClient struct {
	agents     []herdrx.Agent
	pong       herdrx.Pong
	notices    []string
	creates    []herdrx.WorkspaceCreate
	starts     []herdrx.AgentStart
	prompts    []promptCall
	closes     []string
	createErr  error
	startErr   error
	promptErr  error
	closeErr   error
	waitStatus string
	waitErr    error
	getErr     error
	renameErr  error
	renames    []renameCall
	unnamed    bool
}

type renameCall struct {
	Target string
	Name   string
}

type promptCall struct {
	Target string
	Text   string
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
func (f *fakeClient) CreateWorkspace(ctx context.Context, in herdrx.WorkspaceCreate) (herdrx.WorkspaceCreated, error) {
	f.creates = append(f.creates, in)
	if f.createErr != nil {
		return herdrx.WorkspaceCreated{}, f.createErr
	}
	id := fmt.Sprintf("w%d", len(f.creates)+2)
	return herdrx.WorkspaceCreated{
		Workspace: herdrx.Workspace{WorkspaceID: id, Label: in.Label},
		Tab:       herdrx.Tab{TabID: id + ":t1", WorkspaceID: id},
		RootPane:  herdrx.Pane{PaneID: id + ":p1", WorkspaceID: id, TabID: id + ":t1"},
	}, nil
}
func (f *fakeClient) CloseWorkspace(ctx context.Context, workspaceID string) error {
	f.closes = append(f.closes, workspaceID)
	return f.closeErr
}
func (f *fakeClient) StartAgent(ctx context.Context, in herdrx.AgentStart) (herdrx.Agent, error) {
	f.starts = append(f.starts, in)
	if f.startErr != nil {
		return herdrx.Agent{}, f.startErr
	}
	return herdrx.Agent{
		Agent: in.Kind, Name: in.Name, Status: "idle",
		PaneID: in.PaneID, WorkspaceID: strings.Split(in.PaneID, ":")[0],
	}, nil
}
func (f *fakeClient) GetAgent(ctx context.Context, target string) (herdrx.Agent, error) {
	if f.unnamed && len(f.renames) == 0 {
		return herdrx.Agent{}, herdrx.ErrNotFound
	}
	if f.getErr != nil {
		return herdrx.Agent{}, f.getErr
	}
	status := f.waitStatus
	if status == "" {
		status = "idle"
	}
	return herdrx.Agent{Name: target, Status: status, InteractiveReady: status == "idle" || status == "done"}, nil
}
func (f *fakeClient) RenameAgent(ctx context.Context, target, name string) (herdrx.Agent, error) {
	f.renames = append(f.renames, renameCall{Target: target, Name: name})
	if f.renameErr != nil {
		return herdrx.Agent{}, f.renameErr
	}
	return herdrx.Agent{Name: name, PaneID: target, Status: "idle"}, nil
}
func (f *fakeClient) WaitAgent(ctx context.Context, target string, until []string, timeoutMS int) (herdrx.Agent, error) {
	if f.waitErr != nil {
		return herdrx.Agent{}, f.waitErr
	}
	status := f.waitStatus
	if status == "" {
		status = "idle"
	}
	return herdrx.Agent{Name: target, Status: status, InteractiveReady: status == "idle" || status == "done"}, nil
}
func (f *fakeClient) PromptAgent(ctx context.Context, target, text string) (herdrx.Agent, error) {
	f.prompts = append(f.prompts, promptCall{Target: target, Text: text})
	if f.promptErr != nil {
		return herdrx.Agent{}, f.promptErr
	}
	return herdrx.Agent{Name: target, Status: "working"}, nil
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

func TestNewStartsClaudeAutoAndPrompts(t *testing.T) {
	fc := &fakeClient{pong: herdrx.Pong{Version: "0.8.0", Protocol: 19}}
	app, dir, out, errb := newTestApp(t, fc)
	cwd := t.TempDir()

	if code := app.run(dir, "--json", "new", "--cwd", cwd, "--label", "fix-login", "fix the login redirect"); code != 0 {
		t.Fatalf("new: %s", errb.String())
	}
	if len(fc.creates) != 1 || fc.creates[0].Cwd != cwd || fc.creates[0].Label != "fix-login" || fc.creates[0].Focus {
		t.Fatalf("create: %+v", fc.creates)
	}
	if len(fc.starts) != 1 {
		t.Fatalf("starts: %+v", fc.starts)
	}
	st := fc.starts[0]
	if st.Name != "fix-login" || st.Kind != "claude" || st.PaneID != "w3:p1" {
		t.Fatalf("start: %+v", st)
	}
	if len(st.Args) != 2 || st.Args[0] != "--permission-mode" || st.Args[1] != "auto" {
		t.Fatalf("auto args: %v", st.Args)
	}
	if len(fc.prompts) != 1 || fc.prompts[0].Target != "fix-login" || fc.prompts[0].Text != "fix the login redirect" {
		t.Fatalf("prompt: %+v", fc.prompts)
	}
	if len(fc.renames) != 0 {
		t.Fatalf("rename: %+v", fc.renames)
	}
	if len(fc.closes) != 0 {
		t.Fatalf("unexpected close: %v", fc.closes)
	}

	var sp spaces.Space
	if err := json.Unmarshal(out.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	if sp.WorkspaceID != "w3" || sp.PaneID != "w3:p1" || !sp.Auto || sp.Prompt != "fix the login redirect" {
		t.Fatalf("space: %+v", sp)
	}

	out.Reset()
	errb.Reset()
	if code := app.run(dir, "--json", "ls"); code != 0 {
		t.Fatalf("ls: %s", errb.String())
	}
	var list []spaces.Space
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].WorkspaceID != "w3" {
		t.Fatalf("ls: %s", out.String())
	}
}

func TestNewRenamesUnnamedAgentThenPrompts(t *testing.T) {
	fc := &fakeClient{
		pong:    herdrx.Pong{Version: "0.8.0", Protocol: 19},
		unnamed: true,
	}
	app, dir, _, errb := newTestApp(t, fc)

	if code := app.run(dir, "--json", "new", "--cwd", t.TempDir(), "--kind", "grok", "--label", "pull-latest-changes", "pull latest changes"); code != 0 {
		t.Fatalf("new: %s", errb.String())
	}
	if len(fc.renames) != 1 || fc.renames[0].Target != "w3:p1" || fc.renames[0].Name != "pull-latest-changes" {
		t.Fatalf("rename: %+v", fc.renames)
	}
	if len(fc.prompts) != 1 || fc.prompts[0].Target != "pull-latest-changes" {
		t.Fatalf("prompt: %+v", fc.prompts)
	}
}

func TestNewManualAndNoPrompt(t *testing.T) {
	fc := &fakeClient{pong: herdrx.Pong{Version: "0.8.0", Protocol: 19}}
	app, dir, _, errb := newTestApp(t, fc)
	cwd := t.TempDir()

	if code := app.run(dir, "new", "--cwd", cwd, "--label", "review", "--manual", "--focus"); code != 0 {
		t.Fatalf("new: %s", errb.String())
	}
	if len(fc.creates) != 1 || !fc.creates[0].Focus {
		t.Fatalf("create: %+v", fc.creates)
	}
	if len(fc.starts) != 1 || len(fc.starts[0].Args) != 0 {
		t.Fatalf("start args: %+v", fc.starts)
	}
	if len(fc.prompts) != 0 {
		t.Fatalf("prompt: %+v", fc.prompts)
	}
}

func TestNewNonClaudeSkipsAutoArgs(t *testing.T) {
	fc := &fakeClient{pong: herdrx.Pong{Version: "0.8.0", Protocol: 19}}
	app, dir, _, errb := newTestApp(t, fc)
	cwd := t.TempDir()

	if code := app.run(dir, "new", "--cwd", cwd, "--kind", "grok", "--name", "reviewer"); code != 0 {
		t.Fatalf("new: %s", errb.String())
	}
	if len(fc.starts) != 1 || fc.starts[0].Kind != "grok" || len(fc.starts[0].Args) != 0 || fc.starts[0].Name != "reviewer" {
		t.Fatalf("start: %+v", fc.starts)
	}
}

func TestNewRejectsInvalidName(t *testing.T) {
	app, dir, _, errb := newTestApp(t, nil)
	if code := app.run(dir, "--json", "new", "--cwd", t.TempDir(), "--name", "1bad"); code != 1 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "invalid") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestNewRejectsTakenName(t *testing.T) {
	fc := &fakeClient{
		pong:   herdrx.Pong{Version: "0.8.0", Protocol: 19},
		agents: []herdrx.Agent{{Name: "reviewer"}},
	}
	app, dir, _, errb := newTestApp(t, fc)
	if code := app.run(dir, "--json", "new", "--cwd", t.TempDir(), "--name", "reviewer"); code != 1 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "conflict") {
		t.Fatalf("stderr: %s", errb.String())
	}
	if len(fc.creates) != 0 {
		t.Fatalf("created after name conflict: %+v", fc.creates)
	}
}

func TestNewSuffixesGeneratedName(t *testing.T) {
	fc := &fakeClient{
		pong:   herdrx.Pong{Version: "0.8.0", Protocol: 19},
		agents: []herdrx.Agent{{Name: "fix-login"}},
	}
	app, dir, _, errb := newTestApp(t, fc)
	if code := app.run(dir, "new", "--cwd", t.TempDir(), "--label", "fix-login", "--manual"); code != 0 {
		t.Fatalf("new: %s", errb.String())
	}
	if len(fc.starts) != 1 || fc.starts[0].Name != "fix-login-2" {
		t.Fatalf("start: %+v", fc.starts)
	}
}

func TestNewSkipsPromptWhenBlocked(t *testing.T) {
	fc := &fakeClient{
		pong:       herdrx.Pong{Version: "0.8.0", Protocol: 19},
		waitStatus: "blocked",
	}
	app, dir, _, errb := newTestApp(t, fc)
	if code := app.run(dir, "new", "--cwd", t.TempDir(), "--label", "fix-login", "do the thing"); code != 1 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if len(fc.prompts) != 0 {
		t.Fatalf("prompted while blocked: %+v", fc.prompts)
	}
	st, err := spaces.Open(spaces.DirFor(dir, "default"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("w3"); err != nil {
		t.Fatalf("should still record: %v", err)
	}
}

func TestNewPromptFailureStillRecords(t *testing.T) {
	fc := &fakeClient{
		pong:      herdrx.Pong{Version: "0.8.0", Protocol: 19},
		promptErr: fmt.Errorf("stalled"),
	}
	app, dir, _, errb := newTestApp(t, fc)
	if code := app.run(dir, "new", "--cwd", t.TempDir(), "--label", "fix-login", "do the thing"); code != 1 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	st, err := spaces.Open(spaces.DirFor(dir, "default"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("w3")
	if err != nil || got.Name != "fix-login" {
		t.Fatalf("recorded: %v %+v", err, got)
	}
}

func TestNewClosesWorkspaceWhenStartFails(t *testing.T) {
	fc := &fakeClient{
		pong:     herdrx.Pong{Version: "0.8.0", Protocol: 19},
		startErr: fmt.Errorf("boom"),
	}
	app, dir, _, errb := newTestApp(t, fc)
	if code := app.run(dir, "new", "--cwd", t.TempDir(), "--label", "x"); code != 1 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if len(fc.creates) != 1 || len(fc.closes) != 1 || fc.closes[0] != "w3" {
		t.Fatalf("creates=%v closes=%v", fc.creates, fc.closes)
	}
	st, err := spaces.Open(spaces.DirFor(dir, "default"))
	if err != nil {
		t.Fatal(err)
	}
	list, err := st.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("recorded: %v %+v", err, list)
	}
}

func TestDownClosesRecordedWorkspace(t *testing.T) {
	fc := &fakeClient{pong: herdrx.Pong{Version: "0.8.0", Protocol: 19}}
	app, dir, out, errb := newTestApp(t, fc)
	if code := app.run(dir, "new", "--cwd", t.TempDir(), "--label", "fix-login", "--manual"); code != 0 {
		t.Fatalf("new: %s", errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := app.run(dir, "down", "fix-login"); code != 0 {
		t.Fatalf("down: %s", errb.String())
	}
	if len(fc.closes) != 1 || fc.closes[0] != "w3" {
		t.Fatalf("closes: %v out=%s", fc.closes, out.String())
	}
	if !strings.Contains(out.String(), "closed w3") {
		t.Fatalf("out: %s", out.String())
	}

	out.Reset()
	if code := app.run(dir, "ls"); code != 0 {
		t.Fatalf("ls: %s", errb.String())
	}
	if !strings.Contains(out.String(), "(no workspaces)") {
		t.Fatalf("ls after down: %s", out.String())
	}
}

func TestDownUnknown(t *testing.T) {
	app, dir, _, errb := newTestApp(t, nil)
	if code := app.run(dir, "--json", "down", "w9"); code != 1 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "not_found") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestDownRefusesOpenQueueUnlessForced(t *testing.T) {
	fc := &fakeClient{pong: herdrx.Pong{Version: "0.8.0", Protocol: 19}}
	app, dir, _, errb := newTestApp(t, fc)
	if code := app.run(dir, "new", "--cwd", t.TempDir(), "--label", "fix-login", "--manual"); code != 0 {
		t.Fatalf("new: %s", errb.String())
	}

	st, err := queue.Open(queue.DirFor(dir, "default"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Upsert(queue.Draft{
		PaneID:         "w3:p1",
		WorkspaceID:    "w3",
		Name:           "fix-login",
		Kind:           queue.KindNeedsDecision,
		StateChangeSeq: 1,
	}, app.now()); err != nil {
		t.Fatal(err)
	}

	errb.Reset()
	if code := app.run(dir, "--json", "down", "w3"); code != 1 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "conflict") {
		t.Fatalf("stderr: %s", errb.String())
	}
	if len(fc.closes) != 0 {
		t.Fatalf("closed without force: %v", fc.closes)
	}

	errb.Reset()
	if code := app.run(dir, "down", "--force", "w3"); code != 0 {
		t.Fatalf("force: %s", errb.String())
	}
	if len(fc.closes) != 1 {
		t.Fatalf("closes: %v", fc.closes)
	}
	open, err := st.List(false)
	if err != nil || len(open) != 0 {
		t.Fatalf("queue after force: %v %+v", err, open)
	}
}
