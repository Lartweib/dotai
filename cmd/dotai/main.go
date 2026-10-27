// Command dotai is the dotai command line.
package main

import (
	"os"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Version: app.Version}))
}
