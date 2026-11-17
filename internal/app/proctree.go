package app

import "os/exec"

// runTree runs cmd as the root of a tree that cancelling it ends whole
// (prepareTree), and waits for it.
func runTree(cmd *exec.Cmd) error {
	started, done := prepareTree(cmd)
	defer done()
	if err := cmd.Start(); err != nil {
		return err
	}
	started()
	return cmd.Wait()
}
