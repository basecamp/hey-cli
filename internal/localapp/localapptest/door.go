// Package localapptest is a fake of the HEY app's door for tests: a Unix socket that
// answers MCP as the door's contract does, from what a test gives it.
package localapptest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/localapp"
)

// Door answers initialize, tools/list and tools/call. A test offers tools (Offer) and
// sets what a call answers (Answer, Refuse, AnswerRaw), and may make a method answer with
// a JSON-RPC error (Fail), with a line of its own (Reply) or not at all (Hang), to see what
// a client makes of an app that misbehaves.
type Door struct {
	Path string

	mu       sync.Mutex
	tools    []map[string]any
	results  map[string]map[string]any
	errors   map[string]RPCError
	raw      map[string]string
	hang     map[string]bool
	requests []Request
}

type RPCError struct {
	Code    int
	Message string
}

// Request is one message the door read: its method and its params.
type Request struct {
	Method string
	Params json.RawMessage
}

// Start opens a door, closed when the test ends.
func Start(t testing.TB) *Door {
	t.Helper()
	path, listener := Listen(t)
	door := &Door{
		Path:    path,
		results: map[string]map[string]any{},
		errors:  map[string]RPCError{},
		raw:     map[string]string{},
		hang:    map[string]bool{},
	}
	go door.serve(listener)
	return door
}

// Listen opens a Unix socket for a test that answers by hand, closed when the test ends.
// Its folder is short-named under the system's temporary folder, since a socket's path is
// capped near a hundred bytes and a test's own temporary folder on macOS is longer.
func Listen(t testing.TB) (string, *net.UnixListener) {
	t.Helper()
	folder, err := os.MkdirTemp("", "hl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(folder) })

	path := filepath.Join(folder, "agent.sock")
	var config net.ListenConfig
	listener, err := config.Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	unix, ok := listener.(*net.UnixListener)
	if !ok {
		t.Fatalf("a unix listen gave a %T", listener)
	}
	return path, unix
}

// Offer lists a tool, with the effect the door names in its _meta and the arguments its
// schema declares, each required, as the door's are.
func (d *Door) Offer(name, description, effect string, arguments ...string) {
	properties := map[string]any{}
	for _, argument := range arguments {
		properties[argument] = map[string]any{"type": "string"}
	}
	if arguments == nil {
		arguments = []string{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tools = append(d.tools, map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": map[string]any{"type": "object", "properties": properties, "required": arguments, "additionalProperties": false},
		"_meta":       map[string]any{"com.hey/effect": effect},
	})
}

// AnswerRaw makes a tools/call of name answer with result, as written, for a result that
// is not what Answer builds.
func (d *Door) AnswerRaw(name string, result map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.results[name] = result
}

// Answer makes a tools/call of name answer structured, done.
func (d *Door) Answer(name string, structured any) {
	d.answer(name, structured, false)
}

// Refuse makes a tools/call of name answer structured as a refusal (isError).
func (d *Door) Refuse(name string, structured any) {
	d.answer(name, structured, true)
}

// Fail makes method answer with a JSON-RPC error.
func (d *Door) Fail(method string, code int, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.errors[method] = RPCError{Code: code, Message: message}
}

// Reply makes method answer with line, as written, in place of an answer.
func (d *Door) Reply(method, line string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.raw[method] = line
}

// Hang makes method go unanswered, the connection left open.
func (d *Door) Hang(method string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hang[method] = true
}

// Requests is every message the door has read, notifications included, in order.
func (d *Door) Requests() []Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Request(nil), d.requests...)
}

// Calls is the params of every tools/call the door read.
func (d *Door) Calls() []json.RawMessage {
	var calls []json.RawMessage
	for _, request := range d.Requests() {
		if request.Method == "tools/call" {
			calls = append(calls, request.Params)
		}
	}
	return calls
}

func (d *Door) answer(name string, structured any, refused bool) {
	text, _ := json.Marshal(structured)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.results[name] = map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": structured,
		"isError":           refused,
	}
}

func (d *Door) serve(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			continue
		}
		go d.converse(conn)
	}
}

func (d *Door) converse(conn net.Conn) {
	defer conn.Close()
	lines := bufio.NewScanner(conn)
	lines.Buffer(make([]byte, 0, 64<<10), localapp.MaxLine)
	for lines.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(lines.Bytes(), &request); err != nil {
			d.write(conn, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			continue
		}
		d.mu.Lock()
		d.requests = append(d.requests, Request{Method: request.Method, Params: request.Params})
		raw, isRaw := d.raw[request.Method]
		failure, fails := d.errors[request.Method]
		hangs := d.hang[request.Method]
		d.mu.Unlock()

		if len(request.ID) == 0 || hangs {
			continue
		}
		switch {
		case isRaw:
			_, _ = conn.Write([]byte(raw + "\n"))
		case fails:
			d.write(conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": failure.Code, "message": failure.Message}})
		default:
			d.write(conn, d.respond(request.ID, request.Method, request.Params))
		}
	}
}

func (d *Door) respond(id json.RawMessage, method string, params json.RawMessage) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch method {
	case "initialize":
		return result(id, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "hey", "title": "HEY", "version": "0.0.0-test"},
		})
	case "ping":
		return result(id, map[string]any{})
	case "tools/list":
		tools := d.tools
		if tools == nil {
			tools = []map[string]any{}
		}
		return result(id, map[string]any{"tools": tools})
	case "tools/call":
		var call struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(params, &call)
		if answer, ok := d.results[call.Name]; ok {
			return result(id, answer)
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "unknown tool: " + call.Name}}
	default:
		return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "method not found"}}
	}
}

func (d *Door) write(conn net.Conn, value any) {
	line, _ := json.Marshal(value)
	_, _ = conn.Write(append(line, '\n'))
}

func result(id json.RawMessage, value any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": value}
}
