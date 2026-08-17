package watch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/timjonez/herd-orchestrator-cli/internal/classify"
	"github.com/timjonez/herd-orchestrator-cli/internal/herdrx"
	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
)

// Options control the watch loop.
type Options struct {
	Ignore         []string
	SelfPane       string
	NotifyDecision bool
	NotifySettled  bool
	ReconcileEvery time.Duration
	Now            func() time.Time
}

// Event is emitted when the queue changes.
type Event struct {
	Item     queue.Item         `json:"item"`
	Upsert   queue.UpsertResult `json:"upsert"`
	Notified bool               `json:"notified"`
	Reason   string             `json:"reason"`
}

// Loop watches a Herdr session.
type Loop struct {
	Client herdrx.Client
	Queue  *queue.Store
	Opts   Options
	Status func(string)
	Emit   func(Event) error

	seen       map[string]seenAgent
	subscribed map[string]struct{}
	reconnect  chan struct{}
}

type seenAgent struct {
	Status string
	Seq    uint64
}

// Run blocks until ctx is cancelled or a fatal error occurs.
func (l *Loop) Run(ctx context.Context) error {
	if l.Client == nil {
		return fmt.Errorf("watch: nil client")
	}
	if l.Queue == nil {
		return fmt.Errorf("watch: nil queue")
	}
	if l.Opts.ReconcileEvery <= 0 {
		l.Opts.ReconcileEvery = 5 * time.Second
	}
	if l.Opts.Now == nil {
		l.Opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if l.seen == nil {
		l.seen = map[string]seenAgent{}
	}
	if l.reconnect == nil {
		l.reconnect = make(chan struct{}, 1)
	}
	if l.Opts.SelfPane == "" {
		l.Opts.SelfPane = os.Getenv("HERDR_PANE_ID")
	}

	l.statusf("watching %s", l.Client.Socket())

	if err := l.reconcile(ctx); err != nil && !errors.Is(err, context.Canceled) {
		l.statusf("initial list: %v", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		subCtx, cancel := context.WithCancel(ctx)
		subErr := make(chan error, 1)
		go func() {
			subErr <- l.subscribe(subCtx)
		}()

		ticker := time.NewTicker(l.Opts.ReconcileEvery)
		var runErr error
	inner:
		for {
			select {
			case <-ctx.Done():
				cancel()
				<-subErr
				ticker.Stop()
				return ctx.Err()
			case <-l.reconnect:
				l.statusf("agent set changed; resubscribing")
				cancel()
				<-subErr
				ticker.Stop()
				break inner
			case err := <-subErr:
				ticker.Stop()
				cancel()
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
					l.statusf("subscribe: %v", err)
					runErr = err
				}
				break inner
			case <-ticker.C:
				if err := l.reconcile(ctx); err != nil {
					if errors.Is(err, context.Canceled) {
						cancel()
						<-subErr
						ticker.Stop()
						return err
					}
					l.statusf("reconcile: %v", err)
				}
			}
		}
		if runErr != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

func (l *Loop) subscribe(ctx context.Context) error {
	agents, err := l.Client.ListAgents(ctx)
	if err != nil {
		return err
	}
	subs := sessionSubscriptions(agents)
	l.subscribed = paneSet(agents)
	l.statusf("subscribed to %d filters", len(subs))
	return l.Client.Subscribe(ctx, subs, func(ev herdrx.Event) error {
		if ev.NeedsReconcile() {
			return l.reconcile(ctx)
		}
		if ev.StatusChanged() {
			return l.handlePane(ctx, ev.PaneID)
		}
		return nil
	})
}

func sessionSubscriptions(agents []herdrx.Agent) []herdrx.Subscription {
	subs := []herdrx.Subscription{
		{Type: "pane.created"},
		{Type: "pane.closed"},
		{Type: "pane.moved"},
		{Type: "pane.agent_detected"},
	}
	seen := map[string]struct{}{}
	for _, a := range agents {
		if a.PaneID == "" {
			continue
		}
		if _, ok := seen[a.PaneID]; ok {
			continue
		}
		seen[a.PaneID] = struct{}{}
		subs = append(subs, herdrx.Subscription{
			Type:   "pane.agent_status_changed",
			PaneID: a.PaneID,
		})
	}
	return subs
}

func (l *Loop) reconcile(ctx context.Context) error {
	agents, err := l.Client.ListAgents(ctx)
	if err != nil {
		return err
	}
	live := paneSet(agents)
	// Stable order for tests.
	sort.SliceStable(agents, func(i, j int) bool {
		return agents[i].PaneID < agents[j].PaneID
	})
	for _, a := range agents {
		if err := l.consider(ctx, a); err != nil {
			return err
		}
	}
	for pane := range l.seen {
		if _, ok := live[pane]; !ok {
			delete(l.seen, pane)
		}
	}
	if l.subscribed != nil && !sameSet(l.subscribed, live) {
		l.requestReconnect()
	}
	return nil
}

func (l *Loop) requestReconnect() {
	select {
	case l.reconnect <- struct{}{}:
	default:
	}
}

func paneSet(agents []herdrx.Agent) map[string]struct{} {
	out := make(map[string]struct{}, len(agents))
	for _, a := range agents {
		if a.PaneID != "" {
			out[a.PaneID] = struct{}{}
		}
	}
	return out
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func (l *Loop) handlePane(ctx context.Context, paneID string) error {
	if paneID == "" {
		return nil
	}
	agents, err := l.Client.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a.PaneID == paneID {
			return l.consider(ctx, a)
		}
	}
	delete(l.seen, paneID)
	return nil
}

func (l *Loop) consider(ctx context.Context, a herdrx.Agent) error {
	if l.ignored(a) {
		l.seen[a.PaneID] = seenAgent{Status: a.Status, Seq: a.StateChangeSeq}
		return nil
	}
	prev, had := l.seen[a.PaneID]
	if had && prev.Seq == a.StateChangeSeq && prev.Status == a.Status {
		return nil
	}

	if strings.EqualFold(a.Status, "working") {
		l.seen[a.PaneID] = seenAgent{Status: a.Status, Seq: a.StateChangeSeq}
		return nil
	}

	prevStatus := ""
	if had {
		prevStatus = prev.Status
	}

	detection, excerpt := l.readScreens(ctx, a)
	res := classify.Classify(classify.Input{
		Status:     a.Status,
		PrevStatus: prevStatus,
		Title:      a.DisplayTitle(),
		Detection:  detection,
		Excerpt:    excerpt,
	})
	if res.Kind == classify.KindSkip {
		return nil
	}

	item, ups, err := l.Queue.Upsert(queue.Draft{
		PaneID:         a.PaneID,
		TabID:          a.TabID,
		WorkspaceID:    a.WorkspaceID,
		Agent:          a.Agent,
		Name:           a.Name,
		HerdrStatus:    a.Status,
		Kind:           res.Kind,
		StateChangeSeq: a.StateChangeSeq,
		Revision:       a.Revision,
		Title:          a.DisplayTitle(),
		Excerpt:        classify.TruncateExcerpt(excerpt, 2000),
	}, l.Opts.Now())
	if err != nil {
		return err
	}
	l.seen[a.PaneID] = seenAgent{Status: a.Status, Seq: a.StateChangeSeq}
	if ups.Skipped {
		return nil
	}

	notified := false
	if l.shouldNotify(res.Kind, ups) {
		title := notifyTitle(item)
		body := notifyBody(item)
		if _, err := l.Client.Notify(ctx, title, body, "request"); err != nil {
			l.statusf("notify %s: %v", item.Label(), err)
		} else {
			notified = true
			_ = l.Queue.MarkNotified(item.ID, l.Opts.Now())
			item.NotifiedAt = ptrTime(l.Opts.Now())
		}
	}

	if l.Emit != nil {
		if err := l.Emit(Event{
			Item:     item,
			Upsert:   ups,
			Notified: notified,
			Reason:   res.Reason,
		}); err != nil {
			return err
		}
	} else {
		l.statusf("%s %s %s (%s)", item.Kind, item.Label(), item.PaneID, res.Reason)
	}
	return nil
}

func (l *Loop) readScreens(ctx context.Context, a herdrx.Agent) (detection, excerpt string) {
	target := a.PaneID
	if a.Name != "" {
		target = a.Name
	}
	if r, err := l.Client.ReadAgent(ctx, target, "detection", 0); err == nil {
		detection = r.Text
	}
	if r, err := l.Client.ReadAgent(ctx, target, "recent_unwrapped", 40); err == nil {
		excerpt = r.Text
	}
	if excerpt == "" {
		excerpt = detection
	}
	return detection, excerpt
}

func (l *Loop) shouldNotify(kind string, ups queue.UpsertResult) bool {
	if !ups.ShouldNotify && kind != queue.KindSettled {
		return false
	}
	switch kind {
	case queue.KindNeedsDecision:
		return l.Opts.NotifyDecision
	case queue.KindSettled:
		return l.Opts.NotifySettled
	default:
		return false
	}
}

func (l *Loop) ignored(a herdrx.Agent) bool {
	if l.Opts.SelfPane != "" && a.PaneID == l.Opts.SelfPane {
		return true
	}
	for _, raw := range l.Opts.Ignore {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if a.PaneID == t || a.Name == t || a.Agent == t {
			return true
		}
	}
	return false
}

func (l *Loop) statusf(format string, args ...any) {
	if l.Status == nil {
		return
	}
	l.Status(fmt.Sprintf(format, args...))
}

func notifyTitle(it queue.Item) string {
	return it.Label() + " needs a decision"
}

func notifyBody(it queue.Item) string {
	s := strings.TrimSpace(it.Title)
	if s == "" {
		s = firstLine(it.Excerpt)
	}
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 240 {
		s = string([]rune(s)[:240])
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func ptrTime(t time.Time) *time.Time { return &t }
