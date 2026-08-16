package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPClient talks to an MCP server over the streamable-HTTP transport
// (POST JSON-RPC, Mcp-Session-Id header, JSON or SSE responses).
type HTTPClient struct {
	id      string
	baseURL string
	headers map[string]string
	http    *http.Client
	mu      sync.Mutex
	nextID  int
	session string
	tools   []Tool
}

// StartHTTP connects to a streamable-HTTP MCP endpoint and performs the
// initialize handshake.
func StartHTTP(ctx context.Context, cfg ServerConfig) (*HTTPClient, error) {
	if cfg.URL == "" {
		return nil, errors.New("mcp: http transport requires a url")
	}
	c := &HTTPClient{
		id:      cfg.ID,
		baseURL: cfg.URL,
		headers: cfg.Headers,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *HTTPClient) ID() string { return c.id }

func (c *HTTPClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "astra-harness", "version": "0.1.0"},
	}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		return fmt.Errorf("mcp %s: initialize: %w", c.id, err)
	}
	// Best-effort initialized notification (servers accept it without reply).
	note := map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}
	_, _ = c.post(ctx, note)
	return nil
}

// ListTools returns (and caches) the tools advertised by the server.
func (c *HTTPClient) ListTools() ([]Tool, error) {
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

func (c *HTTPClient) ToolDefs() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Tool(nil), c.tools...)
}

func (c *HTTPClient) ToolNames() []string {
	out := make([]string, 0, len(c.tools))
	for _, t := range c.tools {
		out = append(out, t.Name)
	}
	return out
}

// CallTool invokes a tool and returns the concatenated text content.
func (c *HTTPClient) CallTool(ctx context.Context, name string, args map[string]any) (CallResult, error) {
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
			data, _ := json.Marshal(block)
			b.Write(data)
		}
		b.WriteString("\n")
	}
	return CallResult{Text: strings.TrimRight(b.String(), "\n"), IsError: res.IsError}, nil
}

func (c *HTTPClient) Close() error { return nil }

func (c *HTTPClient) call(ctx context.Context, method string, params any, result any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	c.mu.Lock()
	c.nextID++
	msg["id"] = c.nextID
	c.mu.Unlock()
	if params != nil {
		msg["params"] = params
	}
	raw, err := c.post(ctx, msg)
	if err != nil {
		return err
	}
	return handleRPCResponse(raw, result)
}

// post sends one JSON-RPC message and returns the response body as JSON,
// handling both direct JSON and SSE-encoded responses.
func (c *HTTPClient) post(ctx context.Context, msg map[string]any) (json.RawMessage, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	c.mu.Unlock()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp %s: HTTP %d: %s", c.id, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	// initialize responses carry the session id in a response header.
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		c.mu.Lock()
		c.session = session
		c.mu.Unlock()
	}
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return readSSEMessage(resp.Body)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil // e.g. 202 Accepted for notifications
	}
	return json.RawMessage(body), nil
}

// readSSEMessage extracts the first JSON "data:" payload from an SSE stream
// (the non-streaming response form of the streamable-HTTP transport).
func readSSEMessage(r io.Reader) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			return json.RawMessage(data), nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("mcp: empty SSE response")
}
