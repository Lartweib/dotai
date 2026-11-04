package server

import (
	"os/exec"
	"runtime"
)

// OpenBrowser opens url, an address or the path of a page, in the user's
// default browser and does not wait for it. dotai ui gives it the path of
// the page that holds the one-time code (writeLauncher), since a command
// line can be read by the other users of the machine.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// start through cmd would read the & of a query as a command
		// separator; the URL handler takes the address as it is.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
