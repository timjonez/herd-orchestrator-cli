package watch

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/timjonez/herd-orchestrator-cli/internal/herdrx"
	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
)

type fakeClient struct {
	mu      sync.Mutex
	agents  []herdrx.Agent
	reads   map[string]string
	notices []string
	subs    []herdrx.Subscription
	events  chan herdrx.Event
}

func (f *fakeClient) Socket() string { return "/tmp/fake.sock" }
func (f *fakeClient) Ping(ctx context.Context) (herdrx.Pong, error) {
	return herdrx.Pong{Version: "test", Protocol: 19}, nil
}
func (f *fakeClient) ListAgents(ctx context.Context) ([]herdrx.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]herdrx.Agent, len(f.agents))
	copy(out, f.agents)
	return out, nil
}
func (f *fakeClient) ReadAgent(ctx context.Context, target, source string, lines int) (herdrx.Read, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return herdrx.Read{Text: f.reads[target]}, nil
}
func (f *fakeClient) Notify(ctx context.Context, title, body, sound string) (herdrx.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, title)
	return herdrx.Notification{Shown: true, Reason: "shown"}, nil
}
func (f *fakeClient) CreateWorkspace(ctx context.Context, in herdrx.WorkspaceCreate) (herdrx.WorkspaceCreated, error) {
	return herdrx.WorkspaceCreated{}, nil
}
func (f *fakeClient) CloseWorkspace(ctx context.Context, workspaceID string) error {
	return nil
}
func (f *fakeClient) StartAgent(ctx context.Context, in herdrx.AgentStart) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) GetAgent(ctx context.Context, target string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) RenameAgent(ctx context.Context, target, name string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) WaitAgent(ctx context.Context, target string, until []string, timeoutMS int) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) PromptAgent(ctx context.Context, target, text string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) Subscribe(ctx context.Context, subs []herdrx.Subscription, handle func(herdrx.Event) error) error {
	f.mu.Lock()
	f.subs = append([]herdrx.Subscription(nil), subs...)
	f.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-f.events:
			if err := handle(ev); err != nil {
				return err
			}
		}
	}
}

func TestLoopClassifiesAndNotifies(t *testing.T) {
	st, err := queue.Open(filepath.Join(t.TempDir(), "q.json"))
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{
		agents: []herdrx.Agent{
			{
				Agent: "grok", Name: "self", Status: "working",
				PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1",
				StateChangeSeq: 1, Revision: 1,
			},
			{
				Agent: "codex", Name: "reviewer", Status: "blocked",
				PaneID: "w1:p2", TabID: "w1:t1", WorkspaceID: "w1",
				StateChangeSeq: 4, Revision: 8,
				TerminalTitle: "Allow edit?",
			},
		},
		reads: map[string]string{
			"reviewer": "Allow edit to README.md?\n[y/n]",
		},
		events: make(chan herdrx.Event),
	}

	var emitted []Event
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loop := &Loop{
		Client: fc,
		Queue:  st,
		Opts: Options{
			SelfPane:       "w1:p1",
			NotifyDecision: true,
			ReconcileEvery: time.Hour,
			Now:            func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) },
		},
		Emit: func(ev Event) error {
			emitted = append(emitted, ev)
			cancel()
			return nil
		},
	}

	err = loop.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Fatal(err)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted: %+v", emitted)
	}
	if emitted[0].Item.Kind != queue.KindNeedsDecision || emitted[0].Item.Name != "reviewer" {
		t.Fatalf("item: %+v", emitted[0].Item)
	}
	if !emitted[0].Notified {
		t.Fatal("expected notify")
	}
	if len(fc.notices) != 1 {
		t.Fatalf("notices: %v", fc.notices)
	}

	open, err := st.List(false)
	if err != nil || len(open) != 1 || open[0].ID != 1 {
		t.Fatalf("queue: %v %+v", err, open)
	}
}

func TestLoopIgnoresSelfAndWorking(t *testing.T) {
	st, err := queue.Open(filepath.Join(t.TempDir(), "q.json"))
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{
		agents: []herdrx.Agent{
			{Agent: "grok", Name: "boss", Status: "working", PaneID: "w1:p1", StateChangeSeq: 1},
			{Agent: "codex", Name: "impl", Status: "working", PaneID: "w1:p2", StateChangeSeq: 2},
		},
		reads:  map[string]string{},
		events: make(chan herdrx.Event),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	loop := &Loop{
		Client: fc,
		Queue:  st,
		Opts: Options{
			SelfPane:       "w1:p1",
			NotifyDecision: true,
			ReconcileEvery: time.Hour,
		},
		Emit: func(ev Event) error {
			t.Fatalf("unexpected emit %+v", ev)
			return nil
		},
	}
	_ = loop.Run(ctx)
	open, err := st.List(true)
	if err != nil || len(open) != 0 {
		t.Fatalf("queue should be empty: %v %+v", err, open)
	}
}
