package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// streamTestCommand returns a command that prints 1..n on separate lines,
// using syntax that works on both the Windows cmd shell and POSIX sh.
func streamTestCommand(n int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("for /L %%i in (1,1,%d) do @echo %%i", n)
	}
	return fmt.Sprintf("i=1; while [ $i -le %d ]; do echo $i; i=$((i+1)); done", n)
}

// TestRunShellStreamsOutput verifies the exec-style execution model: output is
// pushed as EvToolStream chunks while the command runs (batched by
// streamBatchSize), and the final ToolResult still carries the full capture.
func TestRunShellStreamsOutput(t *testing.T) {
	eng := newTestEngine(t, t.TempDir())
	eng.Perm.SetMode(ModeAllow)

	var (
		mu     sync.Mutex
		chunks []string
		stop   = make(chan struct{})
	)
	go func() {
		for {
			select {
			case <-stop:
				return
			case ev := <-eng.Events:
				if ev.Type == EvToolStream {
					if m, ok := ev.Data.(map[string]any); ok {
						if c, ok := m["chunk"].(string); ok {
							mu.Lock()
							chunks = append(chunks, c)
							mu.Unlock()
						}
					}
				}
			}
		}
	}()
	defer close(stop)

	// 12 lines → 2 batches (streamBatchSize=10) plus the trailing flush.
	args, _ := json.Marshal(map[string]any{
		"command": streamTestCommand(12),
	})
	res := eng.ExecuteTool(context.Background(), "run_command", string(args))
	if !res.Success {
		t.Fatalf("command failed: %s", res.Output)
	}
	if got := strings.Count(res.Output, "\n"); got != 12 {
		t.Fatalf("expected 12 lines in final output, got %d: %q", got, res.Output)
	}

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(chunks)
		joined := strings.Join(chunks, "")
		mu.Unlock()
		if n >= 2 {
			if !strings.Contains(joined, "1\n") || !strings.Contains(joined, "12\n") {
				t.Fatalf("stream chunks missing lines: %q", joined)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected at least 2 stream chunks, got %d: %q", n, joined)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestRunShellTimeoutKillsProcess verifies the timeout still terminates a
// long-running command promptly (no hang) and reports failure.
func TestRunShellTimeoutKillsProcess(t *testing.T) {
	eng := newTestEngine(t, t.TempDir())
	eng.Perm.SetMode(ModeAllow)

	args, _ := json.Marshal(map[string]any{
		"command":         "sleep 30",
		"timeout_seconds": 1,
	})
	start := time.Now()
	res := eng.ExecuteTool(context.Background(), "run_command", string(args))
	elapsed := time.Since(start)

	if res.Success {
		t.Fatal("timed-out command should fail")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("timeout did not terminate promptly: took %s", elapsed)
	}
	if res.Metadata["exit_code"] == "0" {
		t.Fatalf("exit_code should be non-zero, got %s", res.Metadata["exit_code"])
	}
}
