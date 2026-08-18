package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/timjonez/herd-orchestrator-cli/internal/herdrx"
	"github.com/timjonez/herd-orchestrator-cli/internal/queue"
)

// Version is set at build time via -ldflags or defaults here.
var Version = "0.1.0"

// App holds shared CLI state.
type App struct {
	Stdout   io.Writer
	Stderr   io.Writer
	Quiet    bool
	JSON     bool
	Socket   string
	Session  string
	StateDir string

	newClient func(socket string) (herdrx.Client, error)
	openQueue func(path string) (*queue.Store, error)
	now       func() time.Time
	selfPane  string
}

// NewApp constructs an App with process defaults.
func NewApp() *App {
	return &App{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		now:    func() time.Time { return time.Now().UTC() },
		newClient: func(socket string) (herdrx.Client, error) {
			return herdrx.Dial(socket)
		},
		openQueue: queue.Open,
		selfPane:  os.Getenv("HERDR_PANE_ID"),
	}
}

// Execute runs the root command. Returns a process exit code.
func (a *App) Execute(args []string) int {
	root := a.rootCmd()
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	if args != nil {
		root.SetArgs(args)
	}
	if err := root.Execute(); err != nil {
		if !errors.Is(err, errSilent) {
			if a.JSON {
				a.writeJSONError(err)
			} else {
				fmt.Fprintln(a.Stderr, err.Error())
			}
		}
		return 1
	}
	return 0
}

var errSilent = errors.New("silent")

func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "herd",
		Short:         "Watch Herdr agents and queue decisions",
		Long:          "herd is a thin session watcher for Herdr: it classifies agent attention events, keeps a durable pending queue, and pages you when a human decision is needed.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.PersistentFlags().StringVar(&a.Socket, "socket", "", "Herdr Unix socket (default: HERDR_SOCKET_PATH or ~/.config/herdr/herdr.sock)")
	root.PersistentFlags().StringVar(&a.Session, "session", "", "Herdr session name (default: HERDR_SESSION)")
	root.PersistentFlags().StringVar(&a.StateDir, "state-dir", "", "state root for queue and spaces (default: HERD_STATE_DIR or $XDG_DATA_HOME/herd)")
	root.PersistentFlags().BoolVarP(&a.Quiet, "quiet", "q", false, "minimal human output")
	root.PersistentFlags().BoolVar(&a.JSON, "json", false, "JSON output on stdout; errors as JSON on stderr")

	root.AddCommand(a.versionCmd())
	root.AddCommand(a.watchCmd())
	root.AddCommand(a.statusCmd())
	root.AddCommand(a.pendingCmd())
	root.AddCommand(a.showCmd())
	root.AddCommand(a.ackCmd())
	root.AddCommand(a.dismissCmd())
	root.AddCommand(a.notifyCmd())
	root.AddCommand(a.newCmd())
	root.AddCommand(a.lsCmd())
	root.AddCommand(a.downCmd())
	return root
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print herd version",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.emitAlways(map[string]string{"version": Version}, func() {
				fmt.Fprintln(a.Stdout, Version)
			})
		},
	}
}

func (a *App) socketPath() string {
	return herdrx.ResolveSocket(a.Socket, a.Session)
}

func (a *App) sessionKey() string {
	return herdrx.SessionKey(a.Session)
}

func (a *App) stateDir() string {
	if a.StateDir != "" {
		return a.StateDir
	}
	return queue.DefaultStateDir()
}

func (a *App) queuePath() string {
	return queue.DirFor(a.stateDir(), a.sessionKey())
}

func (a *App) store() (*queue.Store, error) {
	return a.openQueue(a.queuePath())
}

func (a *App) client() (herdrx.Client, error) {
	return a.newClient(a.socketPath())
}
