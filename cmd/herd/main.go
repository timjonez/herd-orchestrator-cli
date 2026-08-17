package main

import (
	"os"

	"github.com/timjonez/herd-orchestrator-cli/internal/cli"
)

func main() {
	app := cli.NewApp()
	os.Exit(app.Execute(os.Args[1:]))
}
