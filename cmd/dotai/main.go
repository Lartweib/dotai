// Command dotai is the dotai command line.
package main

import (
	"os"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
)

func main() {
	restore := cli.EnableANSI(os.Stdout)
	code := cli.Run(os.Args[1:], cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Version: app.Version, Validate: app.Validate, Stage: app.Stage, FetchRulesync: app.FetchRulesync, PlanInstall: app.PlanInstall, Install: app.Install,
		Restore: app.Restore, PlanRun: app.PlanRun, Launch: app.Launch, Prompt: cli.TerminalPrompt(os.Stdin, os.Stdout)})
	restore()
	os.Exit(code)
}
