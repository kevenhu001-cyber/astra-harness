package engine

import (
	"context"
	"os"
	"os/exec"
	"runtime"
)

// shellCommand builds a command that runs `command` through the platform
// shell: `sh -c` on Unix-like systems and `cmd /C` on Windows. This keeps
// run_command and lifecycle hooks usable on Windows without a POSIX shell.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(ctx, "cmd", "/C", command)
		cmd.Env = os.Environ()
		return cmd
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = os.Environ()
	return cmd
}
