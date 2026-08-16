package tui

import (
	"context"
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
