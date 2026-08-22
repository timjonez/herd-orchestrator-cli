package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timjonez/herd-orchestrator-cli/internal/herdrx"
	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
	"github.com/timjonez/herd-orchestrator-cli/internal/spaces"
)

func (a *App) newCmd() *cobra.Command {
	var (
		label  string
		cwd    string
		name   string
		kind   string
		manual bool
		focus  bool
	)
	cmd := &cobra.Command{
		Use:   "new [prompt...]",
		Short: "Create a workspace, start Claude, and submit a prompt",
		Long:  "Create a Herdr workspace, start an agent in its root pane, and submit an optional prompt. Does not wait. Claude defaults to --permission-mode auto; pass --manual to skip that.",
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.TrimSpace(strings.Join(args, " "))
			return a.runNew(cmd.Context(), newOpts{
				label:  label,
				cwd:    cwd,
				name:   name,
				kind:   kind,
				manual: manual,
				focus:  focus,
				prompt: prompt,
			})
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "workspace label (default: cwd basename)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (default: current directory)")
	cmd.Flags().StringVar(&name, "name", "", "agent name (default: from label)")
	cmd.Flags().StringVar(&kind, "kind", "claude", "Herdr agent kind")
	cmd.Flags().BoolVar(&manual, "manual", false, "do not pass --permission-mode auto (Claude only)")
	cmd.Flags().BoolVar(&focus, "focus", false, "focus the new workspace")
	return cmd
}

func (a *App) lsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List workspaces created by herd new",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.spaceStore()
			if err != nil {
				return err
			}
			list, err := st.List()
			if err != nil {
				return err
			}
			return a.emitAlways(list, func() {
				printSpaces(a.Stdout, list)
			})
		},
	}
}

func (a *App) downCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "down <workspace|name>",
		Short: "Close a workspace created by herd new",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDown(cmd.Context(), args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "close even if the workspace has open queue items")
	return cmd
}

type newOpts struct {
	label  string
	cwd    string
	name   string
	kind   string
	manual bool
	focus  bool
	prompt string
}

func (a *App) runNew(ctx context.Context, opt newOpts) error {
	cwd, err := resolveCwd(opt.cwd)
	if err != nil {
		return err
	}
	label := strings.TrimSpace(opt.label)
	if label == "" {
		label = filepath.Base(cwd)
	}
	kind := strings.ToLower(strings.TrimSpace(opt.kind))
	if kind == "" {
		kind = "claude"
	}

	c, err := a.client()
	if err != nil {
		return err
	}

	name, err := a.resolveAgentName(ctx, c, opt.name, label, kind)
	if err != nil {
		return err
	}

	created, err := c.CreateWorkspace(ctx, herdrx.WorkspaceCreate{
		Cwd:   cwd,
		Label: label,
		Focus: opt.focus,
	})
	if err != nil {
		return err
	}

	autoArgs := permissionArgs(kind, opt.manual)
	started, err := c.StartAgent(ctx, herdrx.AgentStart{
		Name:   name,
		Kind:   kind,
		PaneID: created.RootPane.PaneID,
		Args:   autoArgs,
	})
	if err != nil {
		if cerr := c.CloseWorkspace(ctx, created.Workspace.WorkspaceID); cerr != nil {
			return fmt.Errorf("start agent: %w (also failed to close %s: %v)", err, created.Workspace.WorkspaceID, cerr)
		}
		return fmt.Errorf("start agent: %w", err)
	}
	if started.Name != "" {
		name = started.Name
	}

	sp := spaces.Space{
		WorkspaceID: created.Workspace.WorkspaceID,
		TabID:       created.Tab.TabID,
		PaneID:      created.RootPane.PaneID,
		Name:        name,
		Kind:        kind,
		Label:       label,
		Cwd:         cwd,
		Auto:        len(autoArgs) > 0,
		Prompt:      opt.prompt,
	}
	st, err := a.spaceStore()
	if err != nil {
		_ = c.CloseWorkspace(ctx, sp.WorkspaceID)
		return err
	}
	if _, err := st.Put(sp, a.now()); err != nil {
		_ = c.CloseWorkspace(ctx, sp.WorkspaceID)
		return err
	}

	if opt.prompt != "" {
		if err := ensureNamed(ctx, c, sp.PaneID, name); err != nil {
			return fmt.Errorf("name agent %s in %s: %w", name, sp.WorkspaceID, err)
		}
		ready, err := waitUntilPromptable(ctx, c, name)
		if err != nil {
			return fmt.Errorf("wait for agent %s in %s: %w", name, sp.WorkspaceID, err)
		}
		if ready.Status == "blocked" {
			return fmt.Errorf("prompt agent %s in %s: agent is blocked", name, sp.WorkspaceID)
		}
		if _, err := c.PromptAgent(ctx, name, opt.prompt); err != nil {
			return fmt.Errorf("prompt agent %s in %s: %w", name, sp.WorkspaceID, err)
		}
	}

	return a.emitAlways(sp, func() {
		printSpace(a.Stdout, sp)
	})
}

func (a *App) resolveAgentName(ctx context.Context, c herdrx.Client, explicit, label, kind string) (string, error) {
	taken := liveAgentNames(ctx, c)
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		if !spaces.ValidAgentName(explicit) {
			return "", fmt.Errorf("%w: agent name %q (want [a-z][a-z0-9_-]{0,31})", spaces.ErrInvalid, explicit)
		}
		for _, n := range taken {
			if n == explicit {
				return "", fmt.Errorf("%w: agent name %q is already in use", spaces.ErrConflict, explicit)
			}
		}
		return explicit, nil
	}
	return spaces.UniqueAgentName(spaces.SanitizeAgentName(label, kind), taken), nil
}

func liveAgentNames(ctx context.Context, c herdrx.Client) []string {
	agents, err := c.ListAgents(ctx)
	if err != nil {
		return nil
	}
	var names []string
	for _, ag := range agents {
		if ag.Name != "" {
			names = append(names, ag.Name)
		}
	}
	return names
}

const promptReadyTimeoutMS = 45000

// ensureNamed makes sure Herdr will accept agent.prompt for name.
// agent.start can return before the pane occupant is a named agent
// (seen with grok: pane is idle, name never attached). Prompting the
// pane id then fails with agent_not_ready.
func ensureNamed(ctx context.Context, c herdrx.Client, paneID, name string) error {
	if _, err := c.GetAgent(ctx, name); err == nil {
		return nil
	}
	if _, err := c.RenameAgent(ctx, paneID, name); err != nil {
		return err
	}
	_, err := c.GetAgent(ctx, name)
	return err
}

func waitUntilPromptable(ctx context.Context, c herdrx.Client, target string) (herdrx.Agent, error) {
	ag, err := c.WaitAgent(ctx, target, []string{"idle", "done", "blocked"}, promptReadyTimeoutMS)
	if err != nil {
		return herdrx.Agent{}, err
	}
	if ag.Status != "" {
		return ag, nil
	}
	return c.GetAgent(ctx, target)
}

func permissionArgs(kind string, manual bool) []string {
	if manual || !strings.EqualFold(kind, "claude") {
		return nil
	}
	return []string{"--permission-mode", "auto"}
}

func resolveCwd(raw string) (string, error) {
	var err error
	if raw == "" {
		raw, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func (a *App) runDown(ctx context.Context, id string, force bool) error {
	st, err := a.spaceStore()
	if err != nil {
		return err
	}
	sp, err := st.Get(id)
	if err != nil {
		return err
	}

	open, err := a.openQueueFor(sp.WorkspaceID)
	if err != nil {
		return err
	}
	if n := len(open); n > 0 && !force {
		return fmt.Errorf("%w: workspace %s has %d open queue items (use --force)", spaces.ErrConflict, sp.WorkspaceID, n)
	}

	c, err := a.client()
	if err != nil {
		return err
	}
	closeErr := c.CloseWorkspace(ctx, sp.WorkspaceID)
	if closeErr != nil && !errors.Is(closeErr, herdrx.ErrNotFound) {
		return closeErr
	}
	alreadyGone := errors.Is(closeErr, herdrx.ErrNotFound)

	if err := a.dismissWorkspaceQueue(sp.WorkspaceID); err != nil {
		return err
	}

	if _, err := st.Remove(sp.WorkspaceID); err != nil {
		return err
	}

	return a.emit(sp, true, func() {
		if alreadyGone {
			fmt.Fprintf(a.Stdout, "removed %s (workspace already closed)\n", sp.WorkspaceID)
			return
		}
		fmt.Fprintf(a.Stdout, "closed %s %s\n", sp.WorkspaceID, sp.Name)
	})
}

func (a *App) openQueueFor(workspaceID string) ([]queue.Item, error) {
	qst, err := a.store()
	if err != nil {
		return nil, err
	}
	items, err := qst.List(false)
	if err != nil {
		return nil, err
	}
	var out []queue.Item
	for _, it := range items {
		if it.WorkspaceID == workspaceID {
			out = append(out, it)
		}
	}
	return out, nil
}

func (a *App) dismissWorkspaceQueue(workspaceID string) error {
	qst, err := a.store()
	if err != nil {
		return err
	}
	items, err := qst.List(false)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.WorkspaceID != workspaceID {
			continue
		}
		if _, err := qst.Dismiss(it.ID, a.now()); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) spaceStore() (*spaces.Store, error) {
	return spaces.Open(spaces.DirFor(a.stateDir(), a.sessionKey()))
}

func printSpaces(w io.Writer, list []spaces.Space) {
	if len(list) == 0 {
		fmt.Fprintln(w, "(no workspaces)")
		return
	}
	type row struct{ ws, agent, kind, cwd string }
	rows := make([]row, 0, len(list))
	widths := [4]int{9, 5, 4, 3}
	headers := [4]string{"Workspace", "Agent", "Kind", "Cwd"}
	for _, sp := range list {
		r := row{ws: sp.WorkspaceID, agent: sp.Name, kind: sp.Kind, cwd: sp.Cwd}
		rows = append(rows, r)
		vals := [4]string{r.ws, r.agent, r.kind, r.cwd}
		for i, v := range vals {
			if len(v) > widths[i] {
				widths[i] = len(v)
			}
		}
	}
	fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n",
		widths[0], headers[0],
		widths[1], headers[1],
		widths[2], headers[2],
		headers[3],
	)
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n",
			widths[0], r.ws,
			widths[1], r.agent,
			widths[2], r.kind,
			r.cwd,
		)
	}
}

func printSpace(w io.Writer, sp spaces.Space) {
	fmt.Fprintf(w, "workspace: %s\n", sp.WorkspaceID)
	fmt.Fprintf(w, "pane:      %s\n", sp.PaneID)
	if sp.Name != "" && sp.Kind != "" && sp.Name != sp.Kind {
		fmt.Fprintf(w, "agent:     %s (%s)\n", sp.Name, sp.Kind)
	} else if sp.Name != "" {
		fmt.Fprintf(w, "agent:     %s\n", sp.Name)
	} else {
		fmt.Fprintf(w, "agent:     %s\n", sp.Kind)
	}
	if sp.Cwd != "" {
		fmt.Fprintf(w, "cwd:       %s\n", sp.Cwd)
	}
	fmt.Fprintf(w, "auto:      %t\n", sp.Auto)
	if sp.Prompt != "" {
		fmt.Fprintf(w, "prompt:    %s\n", oneLine(sp.Prompt, 0))
	}
}
