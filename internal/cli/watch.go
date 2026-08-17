package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/timjonez/herd-orchestrator-cli/internal/watch"
)

func (a *App) watchCmd() *cobra.Command {
	var (
		ignore        []string
		notify        bool
		notifySettled bool
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch session agents and queue attention events",
		Long:  "Subscribe to Herdr agent status changes, classify attention events, write them to the pending queue, and toast needs_decision items. Status goes to stderr. --json writes one JSONL object per new or updated item on stdout.",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.store()
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			loop := &watch.Loop{
				Client: c,
				Queue:  st,
				Opts: watch.Options{
					Ignore:         ignore,
					SelfPane:       a.selfPane,
					NotifyDecision: notify,
					NotifySettled:  notifySettled,
					Now:            a.now,
				},
				Status: func(msg string) {
					if a.Quiet {
						return
					}
					fmt.Fprintln(a.Stderr, msg)
				},
				Emit: func(ev watch.Event) error {
					if a.JSON {
						return a.writeJSON(ev)
					}
					if a.Quiet {
						return nil
					}
					fmt.Fprintf(a.Stdout, "%s  %s  %s  %s\n", ev.Item.Kind, ev.Item.Label(), ev.Item.PaneID, ev.Reason)
					return nil
				},
			}
			err = loop.Run(ctx)
			if err == context.Canceled || err == context.DeadlineExceeded {
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringArrayVar(&ignore, "ignore", nil, "pane id or agent name to skip (repeatable)")
	cmd.Flags().BoolVar(&notify, "notify", true, "toast needs_decision items")
	cmd.Flags().BoolVar(&notifySettled, "notify-settled", false, "toast settled items too")
	return cmd
}
