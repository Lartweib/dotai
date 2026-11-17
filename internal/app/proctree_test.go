package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/proc"
)

// TestProcessTreeHelper is the parent and the child of the process tree
// test, by DOTAI_TREE_HELPER; it does nothing when run alone.
func TestProcessTreeHelper(t *testing.T) {
	switch os.Getenv("DOTAI_TREE_HELPER") {
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeHelper$")
		child.Env = append(os.Environ(), "DOTAI_TREE_HELPER=child")
		if err := child.Start(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "child":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

// TestACancelledCommandTakesItsWholeTree checks that cancelling a command
// run as git is also ends what it started, as git-remote-https.
func TestACancelledCommandTakesItsWholeTree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessTreeHelper$")
	cmd.Env = append(os.Environ(), "DOTAI_TREE_HELPER=parent")
	cmd.WaitDelay = time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pid := make(chan int, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		n, _ := strconv.Atoi(strings.TrimSpace(line))
		pid <- n
	}()
	errc := make(chan error, 1)
	go func() { errc <- runTree(cmd) }()
	var child int
	select {
	case child = <-pid:
	case <-time.After(30 * time.Second):
		t.Fatal("the helper started no child")
	}
	if child == 0 {
		t.Fatal("the helper gave no child pid")
	}
	cancel()
	<-errc
	deadline := time.Now().Add(10 * time.Second)
	for {
		if !alive(t, child) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child %d still runs after its parent was cancelled", child)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func alive(t *testing.T, pid int) bool {
	t.Helper()
	ps, err := proc.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.PID == pid {
			return true
		}
	}
	return false
}
