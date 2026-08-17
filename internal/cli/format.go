package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
)

func printItemTable(w io.Writer, items []queue.Item) {
	if len(items) == 0 {
		fmt.Fprintln(w, "(no items)")
		return
	}
	type row struct{ id, kind, status, agent, pane, title string }
	rows := make([]row, 0, len(items))
	for _, it := range items {
		rows = append(rows, row{
			id:     strconv.Itoa(it.ID),
			kind:   it.Kind,
			status: it.HerdrStatus,
			agent:  it.Label(),
			pane:   it.PaneID,
			title:  oneLine(it.Title, 48),
		})
	}
	headers := [6]string{"ID", "Kind", "Status", "Agent", "Pane", "Title"}
	widths := [6]int{2, 4, 6, 5, 4, 5}
	for _, r := range rows {
		vals := [6]string{r.id, r.kind, r.status, r.agent, r.pane, r.title}
		for i, v := range vals {
			if len(v) > widths[i] {
				widths[i] = len(v)
			}
		}
	}
	fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
		widths[0], headers[0],
		widths[1], headers[1],
		widths[2], headers[2],
		widths[3], headers[3],
		widths[4], headers[4],
		headers[5],
	)
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
			widths[0], r.id,
			widths[1], r.kind,
			widths[2], r.status,
			widths[3], r.agent,
			widths[4], r.pane,
			r.title,
		)
	}
}

func printItem(w io.Writer, it queue.Item) {
	fmt.Fprintf(w, "id:       %d\n", it.ID)
	fmt.Fprintf(w, "kind:     %s\n", it.Kind)
	fmt.Fprintf(w, "status:   %s\n", it.HerdrStatus)
	fmt.Fprintf(w, "agent:    %s\n", formatAgent(it))
	fmt.Fprintf(w, "pane:     %s\n", it.PaneID)
	if it.TabID != "" {
		fmt.Fprintf(w, "tab:      %s\n", it.TabID)
	}
	if it.WorkspaceID != "" {
		fmt.Fprintf(w, "workspace: %s\n", it.WorkspaceID)
	}
	fmt.Fprintf(w, "seq:      %d\n", it.StateChangeSeq)
	if it.Title != "" {
		fmt.Fprintf(w, "title:    %s\n", it.Title)
	}
	fmt.Fprintf(w, "created:  %s\n", formatTime(it.CreatedAt))
	if it.NotifiedAt != nil {
		fmt.Fprintf(w, "notified: %s\n", formatTime(*it.NotifiedAt))
	}
	if it.AckedAt != nil {
		fmt.Fprintf(w, "acked:    %s\n", formatTime(*it.AckedAt))
	}
	if it.DismissedAt != nil {
		fmt.Fprintf(w, "dismissed: %s\n", formatTime(*it.DismissedAt))
	}
	if it.Excerpt != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "excerpt:")
		fmt.Fprintln(w, it.Excerpt)
	}
}

func formatAgent(it queue.Item) string {
	if it.Name != "" && it.Agent != "" && it.Name != it.Agent {
		return it.Name + " (" + it.Agent + ")"
	}
	return it.Label()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len([]rune(s)) > max {
		return string([]rune(s)[:max-1]) + "…"
	}
	return s
}
