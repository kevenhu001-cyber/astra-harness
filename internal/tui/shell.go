package tui

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

// newShellCommand builds a shell command bounded by a 30s timeout.
func newShellCommand(ctx context.Context, dir, command string) *exec.Cmd {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		_ = cancel
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	return cmd
}

// streamShell starts the shell command, returns the *exec.Cmd (for cancel/
// status) plus channels for stdout and stderr lines. The caller is
// responsible for waiting on cmd and closing the channels.
func streamShell(ctx context.Context, dir, command string) (*exec.Cmd, <-chan string, <-chan string, error) {
	cmd := newShellCommand(ctx, dir, command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	outCh := make(chan string, 64)
	errCh := make(chan string, 64)
	go pumpLines(stdout, outCh)
	go pumpLines(stderr, errCh)
	return cmd, outCh, errCh, nil
}

func pumpLines(r io.Reader, ch chan<- string) {
	defer close(ch)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		ch <- sc.Text()
	}
}
