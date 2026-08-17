package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
)

func (a *App) pendingCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List queued attention items",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.store()
			if err != nil {
				return err
			}
			items, err := st.List(all)
			if err != nil {
				return err
			}
			return a.emitAlways(items, func() {
				printItemTable(a.Stdout, items)
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include acked and dismissed items")
	return cmd
}

func (a *App) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := queue.ParseID(args[0])
			if err != nil {
				return err
			}
			st, err := a.store()
			if err != nil {
				return err
			}
			it, err := st.Get(id)
			if err != nil {
				return err
			}
			return a.emitAlways(it, func() {
				printItem(a.Stdout, it)
			})
		},
	}
}

func (a *App) ackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ack <id>",
		Short: "Acknowledge an open queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.closeItem(args[0], true)
		},
	}
}

func (a *App) dismissCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dismiss <id>",
		Short: "Dismiss an open queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.closeItem(args[0], false)
		},
	}
}

func (a *App) closeItem(raw string, ack bool) error {
	id, err := queue.ParseID(raw)
	if err != nil {
		return err
	}
	st, err := a.store()
	if err != nil {
		return err
	}
	var it queue.Item
	if ack {
		it, err = st.Ack(id, a.now())
	} else {
		it, err = st.Dismiss(id, a.now())
	}
	if err != nil {
		return err
	}
	return a.emit(it, true, func() {
		verb := "acked"
		if !ack {
			verb = "dismissed"
		}
		fmt.Fprintf(a.Stdout, "%s %d %s\n", verb, it.ID, it.Label())
	})
}
