// Command astra is the Astra Harness CLI.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kevenhu001-cyber/astra-harness/internal/auth"
	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
	"github.com/kevenhu001-cyber/astra-harness/internal/tui"
)

var version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		runTUI("")
		return
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "run", "r":
		runHeadless(args)
	case "init":
		cmdInit(args)
	case "index":
		cmdIndex(args)
	case "status", "st":
		cmdStatus()
	case "state":
		cmdState()
	case "claims":
		cmdClaims()
	case "unknowns", "unknown", "unk":
		cmdUnknowns()
	case "evidence", "ev":
		cmdEvidence()
	case "actions":
		cmdActions()
	case "explain":
		cmdExplain(args)
	case "verify", "v":
		cmdVerify()
	case "resume", "res":
		cmdResume(args)
	case "doctor":
		cmdDoctor()
	case "login":
		cmdLogin()
	case "logout":
		cmdLogout()
	case "whoami", "me":
		cmdWhoami()
	case "help", "-h", "--help":
		usage()
	case "version", "--version", "-V":
		fmt.Printf("astra %s\n", version)
	default:
		runTUI(strings.Join(os.Args[1:], " "))
	}
}

func usage() {
	fmt.Print(`Astra Harness — uncertainty-driven software engineering runtime

USAGE
  astra                     start the interactive TUI
  astra "prompt"            start the TUI with a prompt
  astra run "prompt"        headless agent run (flags: --yes --permission-mode --provider --model --plan)
  astra init                initialize .astra state and knowledge index
  astra index               rebuild the knowledge index
  astra status              show compiled knowledge state
  astra state               dump full state as JSON
  astra claims              list claims
  astra unknowns            list ranked unknowns
  astra evidence            list evidence
  astra actions             list actions
  astra explain <id>        explain a claim or unknown with evidence
  astra verify              run tests/build and record evidence
  astra resume <session>    resume a saved session in the TUI
  astra doctor              diagnose config, providers and tooling
  astra login               sign in with your Astra account (device flow)
  astra logout              sign out locally
  astra whoami              show the signed-in account
  astra version             print version

ACCOUNT
  The login flow opens the Astra website in your browser for authorization.
  Point it at a self-hosted auth server with the auth_server config key or
  the ASTRA_AUTH_SERVER environment variable.
`)
}

func loadEngine() (*engine.Engine, *engine.Config) {
	root, err := os.Getwd()
	if err != nil {
		fatal("cwd: %v", err)
	}
	cfg, err := engine.EnsureConfig(root)
	if err != nil {
		fatal("config: %v", err)
	}
	eng, err := engine.NewEngineWithProgress(root, cfg, indexProgressToStderr())
	if err != nil {
		fatal("engine: %v", err)
	}
	return eng, cfg
}

// indexProgressToStderr renders a simple live counter for CLI commands that
// build or rebuild the knowledge index. It prints nothing when the index is
// already cached (the callback is never invoked).
func indexProgressToStderr() func(done, total int) {
	started := false
	reported := 0
	return func(done, total int) {
		if total <= 0 {
			return
		}
		if !started {
			started = true
			fmt.Fprintln(os.Stderr, "astra: building knowledge index…")
		}
		if done == total {
			fmt.Fprintf(os.Stderr, "\r  %d/%d files\n", done, total)
			return
		}
		step := total / 100
		if step < 1 {
			step = 1
		}
		if done < reported+step {
			return
		}
		reported = done
		fmt.Fprintf(os.Stderr, "\r  %d/%d files    ", done, total)
	}
}

// ensureLoggedIn gates interactive/agent entry behind the Astra account.
// It reuses the stored credential when valid, otherwise starts the Codex-style
// device flow: opens the official site in the browser, waits for approval,
// saves the token, and only then lets the CLI continue.
func ensureLoggedIn() {
	if cred, err := auth.LoadCredential(); err == nil && cred != nil && cred.Token != "" {
		if cred.ExpiresAt.IsZero() || cred.ExpiresAt.After(time.Now()) {
			return
		}
		_ = auth.ClearCredential()
	}
	server := authServer()
	fmt.Printf("Astra requires sign-in. Opening %s in your browser...\n", server)
	cred, err := auth.Login(context.Background(), server, os.Stdout)
	if err != nil {
		fatal("login required: %v", err)
	}
	if err := auth.SaveCredential(cred); err != nil {
		fatal("save credential: %v", err)
	}
	fmt.Printf("Signed in as %s. Starting Astra...\n", cred.User.Email)
}

func runTUI(prompt string) {
	ensureLoggedIn()
	root := mustGetwd()
	cfg, err := engine.EnsureConfig(root)
	if err != nil {
		fatal("config: %v", err)
	}
	eng, err := tui.RunStartup(root, cfg)
	if err != nil {
		fatal("engine: %v", err)
	}
	defer eng.Close()
	if prompt != "" {
		// Preseed: run the prompt in a goroutine once the TUI starts.
		go func() {
			time.Sleep(150 * time.Millisecond)
			_ = eng.Run(context.Background(), prompt)
		}()
	}
	if err := tui.RunWithOptions(root, cfg, eng, prompt == ""); err != nil {
		fatal("tui: %v", err)
	}
}

func mustGetwd() string {
	root, err := os.Getwd()
	if err != nil {
		fatal("cwd: %v", err)
	}
	return root
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	fs.Parse(args)
	root := mustGetwd()
	cfg, err := engine.EnsureConfig(root)
	if err != nil {
		fatal("config: %v", err)
	}
	eng, err := engine.NewEngineWithProgress(root, cfg, indexProgressToStderr())
	if err != nil {
		fatal("engine: %v", err)
	}
	defer eng.Close()
	fmt.Printf("initialized %s\n", eng.StateDir())
	fmt.Printf("index: %s\n", eng.Index.Stats())
	fmt.Printf("provider: %s (%s)\n", eng.ProviderID(), eng.Model)
}

func cmdIndex(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	fs.Parse(args)
	eng, _ := loadEngine()
	defer eng.Close()
	if err := eng.RebuildIndexWithProgress(indexProgressToStderr()); err != nil {
		fatal("index: %v", err)
	}
	fmt.Println(eng.Index.Stats())
}

func cmdStatus() {
	eng, _ := loadEngine()
	defer eng.Close()
	fmt.Println(eng.CompilerOutput())
}

func cmdState() {
	eng, _ := loadEngine()
	defer eng.Close()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(eng.Store.State); err != nil {
		fatal("state: %v", err)
	}
}

func cmdClaims() {
	eng, _ := loadEngine()
	defer eng.Close()
	if len(eng.Store.State.Claims) == 0 {
		fmt.Println("no claims")
		return
	}
	for _, c := range eng.Store.State.Claims {
		fmt.Printf("[%s] %.0f%% %s %s %s\n", c.Status, c.Confidence*100, c.Subject, c.Predicate, c.Object)
	}
}

func cmdUnknowns() {
	eng, _ := loadEngine()
	defer eng.Close()
	unknowns := core.RankUnknowns(eng.Store.State.Unknowns)
	if len(unknowns) == 0 {
		fmt.Println("no open unknowns")
		return
	}
	for _, u := range unknowns {
		fmt.Printf("[p=%.2f] %s (impact %.0f%%, uncertainty %.0f%%, cost %.0f%%)\n",
			u.Priority, u.Description, u.Impact*100, u.Uncertainty()*100, u.ResolutionCost*100)
	}
}

func cmdEvidence() {
	eng, _ := loadEngine()
	defer eng.Close()
	if len(eng.Store.State.Evidence) == 0 {
		fmt.Println("no evidence")
		return
	}
	for _, ev := range eng.Store.State.Evidence {
		first := strings.ReplaceAll(ev.Content, "\n", " ")
		if len(first) > 120 {
			first = first[:117] + "..."
		}
		fmt.Printf("[%s] %s · %s\n   %s\n", ev.Kind, ev.Source, ev.Status, first)
	}
}

func cmdActions() {
	eng, _ := loadEngine()
	defer eng.Close()
	actions := eng.Store.ActionsRecent(30)
	for i := len(actions) - 1; i >= 0; i-- {
		a := actions[i]
		fmt.Printf("[%s] %s · %s (u=%.2f)\n", a.Status, a.Type, a.Description, a.Utility)
	}
}

func cmdExplain(args []string) {
	fs := flag.NewFlagSet("explain", flag.ExitOnError)
	fs.Parse(args)
	id := fs.Arg(0)
	if id == "" {
		fatal("usage: astra explain <claim-id|unknown-id>")
	}
	eng, _ := loadEngine()
	defer eng.Close()
	for _, c := range eng.Store.State.Claims {
		if c.ID == id {
			fmt.Printf("CLAIM %s\n[%s] %s %s %s (confidence %.0f%%)\ncreated: %s\nsource: %s\n",
				c.ID, c.Status, c.Subject, c.Predicate, c.Object, c.Confidence*100, c.CreatedAt.Format(time.RFC3339), c.Source)
			printLinkedEvidence(eng, c.EvidenceIDs)
			return
		}
	}
	for _, u := range eng.Store.State.Unknowns {
		if u.ID == id {
			fmt.Printf("UNKNOWN %s\n%s\npriority: %.3f (impact %.0f%% × uncertainty %.0f%% × dependency %.0f / cost %.0f%%)\nstatus: %s\nsource: %s\n",
				u.ID, u.Description, u.Priority, u.Impact*100, u.Uncertainty()*100, u.DependencyWeight, u.ResolutionCost*100, u.Status, u.Source)
			return
		}
	}
	fatal("no claim or unknown with id %s", id)
}

func printLinkedEvidence(eng *engine.Engine, ids []string) {
	if len(ids) == 0 {
		fmt.Println("\nno linked evidence")
		return
	}
	byID := map[string]*core.Evidence{}
	for _, ev := range eng.Store.State.Evidence {
		byID[ev.ID] = ev
	}
	for _, id := range ids {
		if ev, ok := byID[id]; ok {
			first := strings.ReplaceAll(ev.Content, "\n", " ")
			if len(first) > 160 {
				first = first[:157] + "..."
			}
			fmt.Printf("\nEVIDENCE %s\n[%s] %s · %s\n%s\n", ev.ID, ev.Kind, ev.Source, ev.Status, first)
		}
	}
}

func cmdVerify() {
	eng, _ := loadEngine()
	defer eng.Close()
	res := eng.Verify(context.Background())
	fmt.Println(res.Output)
	if !res.Success {
		os.Exit(1)
	}
}

func cmdResume(args []string) {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	fs.Parse(args)
	id := fs.Arg(0)
	if id == "" {
		fatal("usage: astra resume <session-id>")
	}
	ensureLoggedIn()
	eng, cfg := loadEngine()
	defer eng.Close()
	sess, err := eng.Store.LoadSession(id)
	if err != nil {
		fatal("load session: %v", err)
	}
	if err := eng.LoadSession(sess); err != nil {
		fatal("resume: %v", err)
	}
	if err := tui.Run(mustGetwd(), cfg, eng); err != nil {
		fatal("tui: %v", err)
	}
}

func runHeadless(args []string) {
	ensureLoggedIn()
	yes := false
	plan := false
	permMode := ""
	provider := ""
	model := ""
	var promptParts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--plan" || a == "-p":
			plan = true
		case a == "--permission-mode" || a == "--permission":
			if i+1 < len(args) {
				i++
				permMode = args[i]
			}
		case strings.HasPrefix(a, "--permission-mode="):
			permMode = strings.TrimPrefix(a, "--permission-mode=")
		case a == "--provider":
			if i+1 < len(args) {
				i++
				provider = args[i]
			}
		case strings.HasPrefix(a, "--provider="):
			provider = strings.TrimPrefix(a, "--provider=")
		case a == "--model":
			if i+1 < len(args) {
				i++
				model = args[i]
			}
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		default:
			promptParts = append(promptParts, a)
		}
	}
	prompt := strings.Join(promptParts, " ")
	if prompt == "" {
		fatal("usage: astra run \"prompt\" [--yes] [--provider id] [--model name]")
	}
	eng, cfg := loadEngine()
	defer eng.Close()
	if permMode != "" {
		eng.Perm.SetMode(permMode)
		cfg.PermissionMode = permMode
	}
	if plan {
		eng.Perm.SetPlanMode(true)
	}
	if provider != "" || model != "" {
		if err := eng.SwitchModel(provider, model); err != nil {
			fatal("model: %v", err)
		}
	}
	if yes {
		eng.SetPermissionPrompt(func(engine.PermissionRequest) (engine.PermissionDecision, error) {
			return engine.PermissionDecision{Allowed: true}, nil
		})
	} else if cfg.PermissionMode == "ask" {
		eng.SetPermissionPrompt(promptTerminal)
	}
	// Consume events for headless output.
	go func() {
		for ev := range eng.Events {
			switch ev.Type {
			case engine.EvDelta:
				if s, ok := ev.Data.(string); ok {
					fmt.Print(s)
					os.Stdout.Sync()
				}
			case engine.EvToolStart:
				if m, ok := ev.Data.(map[string]any); ok {
					fmt.Printf("\n\n⌘ %v\n", m["name"])
				}
			case engine.EvToolStream:
				if m, ok := ev.Data.(map[string]any); ok {
					if chunk, ok := m["chunk"].(string); ok {
						fmt.Print(chunk)
						os.Stdout.Sync()
					}
				}
			case engine.EvToolEnd:
				if m, ok := ev.Data.(map[string]any); ok {
					fmt.Printf("\n[%v] %v\n", m["success"], statusWordForCLI(m["success"].(bool)))
				}
			case engine.EvSystem:
				if s, ok := ev.Data.(string); ok {
					fmt.Printf("\n· %s\n", s)
				}
			case engine.EvError:
				if s, ok := ev.Data.(string); ok {
					fmt.Fprintf(os.Stderr, "\nerror: %s\n", s)
				}
			case engine.EvUnknown:
				fmt.Printf("\n· unknown discovered: %s\n", unknownCLI(ev.Data))
			case engine.EvClaim:
				fmt.Printf("\n· claim: %s\n", claimCLI(ev.Data))
			}
		}
	}()
	if err := eng.Run(context.Background(), prompt); err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
}

func promptTerminal(req engine.PermissionRequest) (engine.PermissionDecision, error) {
	fmt.Fprintf(os.Stderr, "\n[permission] %s: %s (%s)\n", req.Kind, req.Target, req.Risk)
	fmt.Fprintf(os.Stderr, "  command: %s\n  allow? [y/N/a]: ", req.Command)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "y":
		return engine.PermissionDecision{Allowed: true}, nil
	case "a":
		return engine.PermissionDecision{Allowed: true, Always: true}, nil
	default:
		return engine.PermissionDecision{Allowed: false}, nil
	}
}

func statusWordForCLI(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func unknownCLI(v any) string {
	if u, ok := v.(*core.Unknown); ok {
		return fmt.Sprintf("%s (p=%.2f)", u.Description, u.Priority)
	}
	return fmt.Sprint(v)
}

func claimCLI(v any) string {
	if c, ok := v.(*core.Claim); ok {
		return fmt.Sprintf("[%s] %s %s %s", c.Status, c.Subject, c.Predicate, c.Object)
	}
	return fmt.Sprint(v)
}

func cmdDoctor() {
	root, err := os.Getwd()
	if err != nil {
		fatal("cwd: %v", err)
	}
	fmt.Printf("root:      %s\n", root)
	cfg, err := engine.LoadConfig(root)
	if err != nil {
		fmt.Printf("config:    FAIL (%v)\n", err)
	} else {
		fmt.Printf("config:    %s (%d providers)\n", filepath.Join(root, ".astra", "config.json"), len(cfg.Providers))
	}
	if cfg == nil {
		cfg = &engine.Config{}
	}
	providers := engine.BuildProviders(cfg)
	for _, p := range providers {
		state := "missing key"
		if p.Available() {
			state = "ready"
		}
		fmt.Printf("provider:  %-10s %s (%s)\n", p.ID(), state, p.Name())
	}
	eng, err := engine.NewEngine(root, cfg)
	if err != nil {
		fmt.Printf("engine:    FAIL (%v)\n", err)
	} else {
		defer eng.Close()
		fmt.Printf("engine:    ok (model %s)\n", eng.Model)
		fmt.Printf("index:     %s\n", eng.Index.Stats())
	}
}

// authServer resolves the auth server base URL: env > project config > default.
func authServer() string {
	if v := os.Getenv("ASTRA_AUTH_SERVER"); v != "" {
		return v
	}
	if root, err := os.Getwd(); err == nil {
		if cfg, err := engine.LoadConfig(root); err == nil && cfg.AuthServer != "" {
			return cfg.AuthServer
		}
	}
	return auth.DefaultServer
}

func cmdLogin() {
	server := authServer()
	fmt.Printf("signing in to %s\n", server)
	cred, err := auth.Login(context.Background(), server, os.Stdout)
	if err != nil {
		fatal("login: %v", err)
	}
	if err := auth.SaveCredential(cred); err != nil {
		fatal("save credential: %v", err)
	}
	fmt.Printf("\nlogged in as %s\n", cred.User.Email)
}

func cmdLogout() {
	if err := auth.ClearCredential(); err != nil {
		fatal("logout: %v", err)
	}
	fmt.Println("logged out")
}

func cmdWhoami() {
	cred, err := auth.LoadCredential()
	if err != nil {
		fatal("whoami: %v", err)
	}
	if cred == nil {
		fatal("not logged in — run `astra login`")
	}
	c := auth.New(cred.Server)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u, err := c.Me(ctx, cred.Token)
	if err != nil {
		fmt.Printf("%s (cached; server unreachable: %v)\n", cred.User.Email, err)
		return
	}
	if u == nil {
		fatal("session invalid or expired — run `astra login`")
	}
	fmt.Printf("%s (%s)\n", u.Email, cred.Server)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "astra: "+format+"\n", args...)
	os.Exit(1)
}
