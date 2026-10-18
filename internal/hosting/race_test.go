package hosting_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/hosting"
)

// Many processes started at once on one state directory end with exactly one
// host and everybody else attached to it. These bugs show up in the wild about
// once a month and cannot be debugged after the fact, so the race is run here,
// many times over (#208). The lock lives on an open file description, so
// goroutines of one process contend for it as processes would.
func TestReach_ManyAtOnceMakeOneHost(t *testing.T) {
	const starters, rounds = 16, 10
	for round := range rounds {
		dir := stateDir(t)
		results := make([]hosting.Reached, starters)
		errs := make([]error, starters)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range starters {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				results[i], errs[i] = hosting.Reach(ctx, app, dir)
			}()
		}
		close(start)
		wg.Wait()

		hosts := 0
		for i, r := range results {
			require.NoError(t, errs[i], "round %d, starter %d", round, i)
			if r.Listener != nil {
				hosts++
			}
		}
		assert.Equal(t, 1, hosts, "round %d", round)
		for _, r := range results {
			r.Close()
		}
	}
}

// childDir names the state directory a child process reaches; set only in the
// processes TestReach_ManyProcessesAtOnceMakeOneHost starts.
const childDir = "TELE_HOSTING_CHILD_DIR"

// The same race across real processes, which is the case in the wild: the lock
// is the operating system's here, not one process's file table.
func TestReach_ManyProcessesAtOnceMakeOneHost(t *testing.T) {
	if testing.Short() {
		t.Skip("starts processes")
	}
	const starters = 8
	dir := stateDir(t)
	type child struct {
		cmd   *exec.Cmd
		stdin io.WriteCloser
		said  *bufio.Scanner
	}
	children := make([]child, starters)
	for i := range children {
		cmd := exec.Command(os.Args[0], "-test.run=^TestReachChild$")
		cmd.Env = append(os.Environ(), childDir+"="+dir)
		stdin, err := cmd.StdinPipe()
		require.NoError(t, err)
		stdout, err := cmd.StdoutPipe()
		require.NoError(t, err)
		children[i] = child{cmd: cmd, stdin: stdin, said: bufio.NewScanner(stdout)}
	}
	for _, c := range children {
		require.NoError(t, c.cmd.Start())
	}

	hosts, clients := 0, 0
	for i, c := range children {
		require.True(t, c.said.Scan(), "child %d said nothing", i)
		switch line := c.said.Text(); line {
		case "host":
			hosts++
		case "client":
			clients++
		default:
			t.Errorf("child %d: %s", i, line)
		}
	}
	for _, c := range children {
		_ = c.stdin.Close()
	}
	for _, c := range children {
		_ = c.cmd.Wait()
	}

	assert.Equal(t, 1, hosts)
	assert.Equal(t, starters-1, clients)
}

// TestReachChild is one process of the race above. It says what it became and
// keeps it until its stdin closes, so the host is still there for everyone
// else to reach.
func TestReachChild(t *testing.T) {
	dir := os.Getenv(childDir)
	if dir == "" {
		t.Skip("run only as a child of the race across processes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := hosting.Reach(ctx, app, dir)
	switch {
	case err != nil:
		fmt.Println("error:", err)
	case r.Listener != nil:
		fmt.Println("host")
	default:
		fmt.Println("client")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	r.Close()
}
