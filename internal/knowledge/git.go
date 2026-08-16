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

// BranchOr returns the current branch name, falling back to "(detached)" or
// a short sha when HEAD does not point at a branch ref.
func (g *Git) BranchOr(fallback string) string {
	b := g.Branch()
	if b == "" || b == "HEAD" {
		if fallback != "" {
			return fallback
		}
		return g.Head()
	}
	return b
}

// Commit creates a commit with the given message, staging all changes.
func (g *Git) Commit(message string) (string, error) {
	if _, err := g.run(context.Background(), "add", "-A"); err != nil {
		return "", err
	}
	out, err := g.run(context.Background(), "commit", "-m", message)
	if err != nil {
		return "", err
	}
	return out, nil
}

// SwitchBranch switches to or creates the named branch.
func (g *Git) SwitchBranch(name string) (string, error) {
	if !strings.ContainsAny(name, "/") && !strings.Contains(name, "_") && name != "" {
		// try checkout -b only if the branch doesn't already exist
		if _, err := g.run(context.Background(), "rev-parse", "--verify", "refs/heads/"+name); err != nil {
			out, cerr := g.run(context.Background(), "checkout", "-b", name)
			if cerr != nil {
				return "", cerr
			}
			return out, nil
		}
	}
	out, err := g.run(context.Background(), "checkout", name)
	if err != nil {
		return "", err
	}
	return out, nil
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
