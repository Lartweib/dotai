// Command dotai is the dotai command line.
package main

import (
	"context"
	"os"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/server"
)

func main() {
	restore := cli.EnableANSI(os.Stdout)
	code := cli.Run(os.Args[1:], cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Version: app.Version, Validate: app.Validate, Stage: app.Stage, FetchRulesync: app.FetchRulesync, PlanInstall: app.PlanInstall, Install: app.Install, PlanProject: app.PlanProject, Apply: app.Apply, RestoreProject: app.RestoreProject, Doctor: app.Doctor,
		Restore: app.Restore, PlanRun: app.PlanRun, Launch: app.Launch, Prompt: cli.TerminalPrompt(os.Stdin, os.Stdout),
		UI: func(ctx context.Context, o cli.UIOptions) error {
			return server.Run(ctx, server.Options{Home: o.Home, CacheDir: o.CacheDir, Port: o.Port, Ready: o.Ready}, server.AppDeps(app.Ports{}))
		},
		OpenBrowser: server.OpenBrowser})
	restore()
	os.Exit(code)
}
