// Package mcp implements a minimal Model Context Protocol (MCP) client over
// stdio, mirroring Codex's MCP support (codex-rs/core/src/mcp.rs): it spawns a
// server process, performs the initialize handshake, lists tools and forwards
// tool calls as JSON-RPC 2.0 messages.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// ProtocolVersion is the MCP protocol revision spoken by this client.
const ProtocolVersion = "2024-11-05"

// ServerConfig describes how to reach an MCP server: either a stdio process
// (Type "stdio", default — Command/Args/Env) or a streamable-HTTP endpoint
// (Type "http" — URL/Headers). Mirrors Codex's McpServerConfig shape.
type ServerConfig struct {
	ID      string            `json:"id"`
	Type    string            `json:"type,omitempty"` // "stdio" | "http"
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Tool is a tool advertised by an MCP server (tools/list result item).
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// CallResult is the normalized outcome of a tools/call.
type CallResult struct {
	Text    string
	IsError bool
}

// ToolClient is the transport-agnostic surface the engine uses to talk to a
// connected MCP server (stdio or HTTP).
type ToolClient interface {
	ID() string
	ListTools() ([]Tool, error)
	ToolNames() []string
	ToolDefs() []Tool
	CallTool(ctx context.Context, name string, args map[string]any) (CallResult, error)
	Close() error
}

var _ ToolClient = (*Client)(nil)
var _ ToolClient = (*HTTPClient)(nil)

// Client is a single stdio MCP server connection.
type Client struct {
	mu        sync.Mutex
	cfg       ServerConfig
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	reader    *bufio.Reader
	nextID    int
	pending   map[int]chan json.RawMessage
	done      chan struct{}
	closeOnce sync.Once
	writeMu   sync.Mutex
	tools     []Tool
}

// Start spawns cfg and performs the MCP initialize handshake.
func Start(ctx context.Context, cfg ServerConfig) (*Client, error) {
	if cfg.Command == "" {
		return nil, errors.New("mcp: command is required")
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: start: %w", cfg.ID, err)
	}
	c := &Client{
		cfg:     cfg,
		cmd:     cmd,
		stdin:   stdin,
		reader:  bufio.NewReaderSize(stdout, 64*1024),
		pending: map[int]chan json.RawMessage{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// startWithIO wires a client to caller-provided streams (used by tests to
// simulate a server without spawning a process).
func startWithIO(ctx context.Context, cfg ServerConfig, stdin io.WriteCloser, stdout io.Reader) (*Client, error) {
	c := &Client{
		cfg:     cfg,
		stdin:   stdin,
		reader:  bufio.NewReaderSize(stdout, 64*1024),
		pending: map[int]chan json.RawMessage{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// ListTools returns (and caches) the tools advertised by the server.
func (c *Client) ListTools() ([]Tool, error) {
	var res struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(context.Background(), "tools/list", nil, &res); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.tools = res.Tools
	c.mu.Unlock()
	return res.Tools, nil
}

// ToolDefs returns the cached advertised tools.
func (c *Client) ToolDefs() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Tool(nil), c.tools...)
}

// CallTool invokes a tool and returns the concatenated text content.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call(ctx, "tools/call", params, &res); err != nil {
		return CallResult{}, err
	}
	var b strings.Builder
	for _, block := range res.Content {
		switch block.Type {
		case "text", "":
			b.WriteString(block.Text)
		default:
			// Preserve structured content as JSON so nothing is silently lost.
			data, _ := json.Marshal(block)
			b.Write(data)
		}
		b.WriteString("\n")
	}
	return CallResult{Text: strings.TrimRight(b.String(), "\n"), IsError: res.IsError}, nil
}

// Close terminates the server process.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		if c.stdin != nil {
			c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			c.cmd.Process.Kill()
			_ = c.cmd.Wait()
		}
	})
	return err
}

// ID returns the configured server id.
func (c *Client) ID() string { return c.cfg.ID }

// ToolNames returns the names of advertised tools.
func (c *Client) ToolNames() []string {
	out := make([]string, 0, len(c.tools))
	for _, t := range c.tools {
		out = append(out, t.Name)
	}
	return out
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "astra-harness", "version": "0.1.0"},
	}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		return fmt.Errorf("mcp %s: initialize: %w", c.cfg.ID, err)
	}
	// Notify the server that initialization is complete.
	note := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.writeLine(note)
	return err
}

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	c.writeMu.Lock()
	_, err := c.writeLine(msg)
	c.writeMu.Unlock()
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("mcp: server closed")
	case raw := <-ch:
		return c.handleResponse(raw, result)
	}
}

func (c *Client) handleResponse(raw json.RawMessage, result any) error {
	return handleRPCResponse(raw, result)
}

// handleRPCResponse decodes a JSON-RPC 2.0 response envelope shared by both
// the stdio and HTTP transports.
func handleRPCResponse(raw json.RawMessage, result any) error {
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Error != nil {
		return fmt.Errorf("mcp: %s (code %d)", env.Error.Message, env.Error.Code)
	}
	if result != nil && env.Result != nil {
		return json.Unmarshal(env.Result, result)
	}
	return nil
}

func (c *Client) writeLine(msg map[string]any) (int, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	data = append(data, '\n')
	return c.stdin.Write(data)
}

// readLoop reads inbound JSON-RPC messages and routes them to pending callers.
// Server-initiated requests without a pending id are answered with a
// method-not-found error per the JSON-RPC spec.
func (c *Client) readLoop() {
	for {
		raw, err := c.readMessage()
		if err != nil {
			c.failAll(err)
			return
		}
		var env struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		if env.ID == nil {
			continue // server notification; nothing to do
		}
		var id int
		if err := json.Unmarshal(*env.ID, &id); err != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		c.mu.Unlock()
		if ok {
			select {
			case ch <- raw:
			case <-c.done:
				return
			}
			continue
		}
		// Unknown request id: answer with method-not-found.
		reply := map[string]any{
			"jsonrpc": "2.0",
			"id":      env.ID,
			"error":   map[string]any{"code": -32601, "message": "method not found"},
		}
		c.writeMu.Lock()
		_, _ = c.writeLine(reply)
		c.writeMu.Unlock()
	}
}

// readMessage reads one JSON-RPC message, supporting both newline-delimited
// and Content-Length framed transports.
func (c *Client) readMessage() (json.RawMessage, error) {
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Content-Length:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "Content-Length:")))
			if err != nil {
				continue
			}
			// Drain headers until the blank line.
			for {
				hdr, err := c.reader.ReadString('\n')
				if err != nil {
					return nil, err
				}
				if strings.TrimSpace(hdr) == "" {
					break
				}
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(c.reader, buf); err != nil {
				return nil, err
			}
			return json.RawMessage(buf), nil
		}
		if !json.Valid(bytes.TrimSpace([]byte(trimmed))) {
			continue
		}
		return json.RawMessage(trimmed), nil
	}
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[int]chan json.RawMessage{}
	c.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- json.RawMessage(`{"error":{"code":-32000,"message":` + strconv.Quote(err.Error()) + `}}`):
		default:
		}
	}
}
