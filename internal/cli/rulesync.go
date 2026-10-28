package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/cli/output"
)

func newRulesyncCmd(env Env, jsonOut *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rulesync",
		Short: "Manage the rulesync binary a pack pins",
	}
	fetch := &cobra.Command{
		Use:   "fetch [path]",
		Short: "Download and verify the rulesync binary the pack in path pins",
		Long: "Download the rulesync binary the pack in path (default: current directory) pins into the dotai cache\n" +
			"(DOTAI_CACHE_DIR, or dotai in the user cache directory) and verify it against the checksum built into dotai.",
		Args: cobra.MaximumNArgs(1),
	}
	format := formatFlag(fetch)
	fetch.RunE = func(cmd *cobra.Command, args []string) error {
		res, err := env.FetchRulesync(packDir(args))
		if err != nil {
			return err
		}
		if len(res.Diagnostics) > 0 {
			return rejectPack(env, *jsonOut, *format, res.Pack, res.Diagnostics, res)
		}
		if *jsonOut {
			return output.Write(env.Stdout, output.Success(commandName(cmd), res))
		}
		how := "downloaded"
		if res.Cached {
			how = "cached"
		}
		_, err = fmt.Fprintf(env.Stdout, "rulesync %s (%s)\nasset:  %s\npath:   %s\nsha256: %s\n",
			res.Version, how, res.Asset, res.Path, res.SHA256)
		return err
	}
	cmd.AddCommand(fetch)
	return cmd
}
