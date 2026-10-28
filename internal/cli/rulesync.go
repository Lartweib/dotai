package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
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
		cache, err := cacheDir()
		if err != nil {
			return err
		}
		ctx, stop := interruptible(cmd)
		defer stop()
		res, err := env.FetchRulesync(ctx, cache, packDir(args), downloadProgress(env, *jsonOut))
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

// interruptible is the context of cmd, cancelled on an interrupt. SIGTERM
// is also delivered on Windows, for a closed console.
func interruptible(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
}

// downloadProgress announces a rulesync download on stderr; it is nil for
// JSON output.
func downloadProgress(env Env, jsonOut bool) app.Progress {
	if jsonOut {
		return nil
	}
	return func(version string, size int64) {
		fmt.Fprintf(env.Stderr, "downloading rulesync %s%s...\n", version, megabytes(size))
	}
}

// cacheDir is DOTAI_CACHE_DIR when set, else dotai in the user cache
// directory, as an absolute path.
func cacheDir() (string, error) {
	d := os.Getenv("DOTAI_CACHE_DIR")
	if d == "" {
		u, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(u, "dotai")
	}
	return filepath.Abs(d)
}

// megabytes formats a download size as " (N MB)", or "" when unknown.
func megabytes(size int64) string {
	if size < 0 {
		return ""
	}
	return fmt.Sprintf(" (%d MB)", (size+500_000)/1_000_000)
}
