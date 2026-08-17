package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timjonez/herd-orchestrator-cli/internal/herdrx"
)

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show watcher, queue, and Herdr connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.store()
			if err != nil {
				return err
			}
			open, err := st.List(false)
			if err != nil {
				return err
			}

			out := statusJSON{
				Socket:   a.socketPath(),
				Session:  a.sessionKey(),
				Queue:    st.Path(),
				Pending:  len(open),
				ByKind:   map[string]int{},
				ByStatus: map[string]int{},
				Agents:   []agentStatus{},
			}
			for _, it := range open {
				out.ByKind[it.Kind]++
			}

			c, err := a.client()
			if err != nil {
				out.Error = err.Error()
			} else {
				if pong, err := c.Ping(cmd.Context()); err != nil {
					out.Error = err.Error()
				} else {
					out.HerdrVersion = pong.Version
					out.Protocol = pong.Protocol
				}
				if agents, err := c.ListAgents(cmd.Context()); err != nil {
					if out.Error == "" {
						out.Error = err.Error()
					}
				} else {
					for _, ag := range agents {
						out.ByStatus[ag.Status]++
						out.Agents = append(out.Agents, agentStatus{
							PaneID:  ag.PaneID,
							Name:    ag.Label(),
							Agent:   ag.Agent,
							Status:  ag.Status,
							Ignored: a.isIgnored(ag, nil),
						})
					}
				}
			}

			return a.emitAlways(out, func() {
				printStatus(a.Stdout, out)
			})
		},
	}
}

type statusJSON struct {
	Socket       string         `json:"socket"`
	Session      string         `json:"session"`
	Queue        string         `json:"queue"`
	HerdrVersion string         `json:"herdr_version,omitempty"`
	Protocol     uint32         `json:"protocol,omitempty"`
	Pending      int            `json:"pending"`
	ByKind       map[string]int `json:"by_kind"`
	ByStatus     map[string]int `json:"by_status"`
	Agents       []agentStatus  `json:"agents"`
	Error        string         `json:"error,omitempty"`
}

type agentStatus struct {
	PaneID  string `json:"pane_id"`
	Name    string `json:"name"`
	Agent   string `json:"agent"`
	Status  string `json:"status"`
	Ignored bool   `json:"ignored"`
}

func printStatus(w interface{ Write([]byte) (int, error) }, s statusJSON) {
	fmt.Fprintf(w, "socket:  %s\n", s.Socket)
	fmt.Fprintf(w, "session: %s\n", s.Session)
	fmt.Fprintf(w, "queue:   %s\n", s.Queue)
	if s.HerdrVersion != "" {
		fmt.Fprintf(w, "herdr:   %s (protocol %d)\n", s.HerdrVersion, s.Protocol)
	}
	if s.Error != "" {
		fmt.Fprintf(w, "error:   %s\n", s.Error)
	}
	fmt.Fprintf(w, "pending: %d\n", s.Pending)
	if len(s.ByKind) > 0 {
		fmt.Fprintf(w, "kinds:   %s\n", formatCounts(s.ByKind))
	}
	if len(s.Agents) == 0 {
		fmt.Fprintln(w, "agents:  (none)")
		return
	}
	fmt.Fprintln(w, "agents:")
	for _, ag := range s.Agents {
		mark := ""
		if ag.Ignored {
			mark = " (ignored)"
		}
		fmt.Fprintf(w, "  %-8s %-16s %s%s\n", ag.PaneID, ag.Name, ag.Status, mark)
	}
}

func formatCounts(m map[string]int) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%d", k, v))
	}
	return strings.Join(parts, " ")
}

func (a *App) isIgnored(ag herdrx.Agent, extra []string) bool {
	if a.selfPane != "" && ag.PaneID == a.selfPane {
		return true
	}
	for _, t := range extra {
		if ag.PaneID == t || ag.Name == t || ag.Agent == t {
			return true
		}
	}
	return false
}
