package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) notifyCmd() *cobra.Command {
	var (
		body  string
		sound string
	)
	cmd := &cobra.Command{
		Use:   "notify --title TEXT",
		Short: "Show a Herdr notification",
		RunE: func(cmd *cobra.Command, args []string) error {
			title, err := cmd.Flags().GetString("title")
			if err != nil {
				return err
			}
			if title == "" {
				return fmt.Errorf("title is required")
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			n, err := c.Notify(cmd.Context(), title, body, sound)
			if err != nil {
				return err
			}
			return a.emit(n, true, func() {
				fmt.Fprintf(a.Stdout, "shown=%t reason=%s\n", n.Shown, n.Reason)
			})
		},
	}
	cmd.Flags().String("title", "", "notification title")
	cmd.Flags().StringVar(&body, "body", "", "notification body")
	cmd.Flags().StringVar(&sound, "sound", "request", "sound: none, done, or request")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}
