package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strconv"
	"strings"
)

// Git wraps the git binary with short timeouts.
type Git struct {
	Root string
}

func (g *Git) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Root
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", &GitError{Err: err, Stderr: errb.String()}
	}
	return strings.TrimSpace(out.String()), nil
}

// Output runs git with an explicit output buffer and returns raw output.
func (g *Git) Output(args ...string) (string, error) {
	return g.run(context.Background(), args...)
}

type GitError struct {
	Err    error
	Stderr string
}

func (e *GitError) Error() string {
	if e.Stderr != "" {
		return strings.TrimSpace(e.Stderr)
	}
	return e.Err.Error()
}

func (g *Git) IsRepo() bool {
	_, err := g.run(context.Background(), "rev-parse", "--is-inside-work-tree")
	return err == nil
}

func (g *Git) Branch() string {
	b, _ := g.run(context.Background(), "rev-parse", "--abbrev-ref", "HEAD")
	return b
}

func (g *Git) Head() string {
	h, _ := g.run(context.Background(), "rev-parse", "--short", "HEAD")
	return h
}

func (g *Git) DefaultBranch() string {
	b, _ := g.run(context.Background(), "symbolic-ref", "refs/remotes/origin/HEAD")
	b = strings.TrimPrefix(b, "refs/remotes/origin/")
	if b == "" || b == "HEAD" {
		b, _ = g.run(context.Background(), "rev-parse", "--abbrev-ref", "HEAD")
	}
	if b == "HEAD" || b == "" {
		return "main"
	}
	return b
}

// StateHash produces a cheap hash of the working tree + HEAD for evidence
// validity checks.
func (g *Git) StateHash() string {
	head := g.Head()
	status, _ := g.run(context.Background(), "status", "--porcelain")
	return shortHash(head + "|" + status)
}

func (g *Git) TrackedFiles() []string {
	out, err := g.run(context.Background(), "ls-files")
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return files
}

func (g *Git) Status() string {
	s, _ := g.run(context.Background(), "status", "--short")
	return s
}

func (g *Git) Diff() string {
	d, _ := g.run(context.Background(), "diff")
	return d
}

func (g *Git) DiffStat() string {
	d, _ := g.run(context.Background(), "diff", "--stat")
	return d
}

func (g *Git) Log(n int) string {
	d, _ := g.run(context.Background(), "log", "--oneline", "-n", strconv.Itoa(n))
	return d
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
