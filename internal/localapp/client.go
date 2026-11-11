// Package localapp speaks MCP to the HEY desktop app's own socket: the door the app opens
// while "Let agents use HEY" is on in its Settings. It is plain MCP, JSON-RPC 2.0 with one
// message per line, and the client asks only for what hey local needs: initialize,
// tools/list and tools/call.
//
// What the door serves is the app's, not HEY's: it runs the app's own commands over the
// mail the app holds, and a command that reaches people (a send) answers pending, waiting
// in the app's window for the person to press Send. Nothing here can confirm one.
package localapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/basecamp/hey-cli/internal/version"
)

// ProtocolVersion is the MCP revision the client asks for. initialize, tools/list and
// tools/call read the same in every revision so far, so whichever one the app answers
// with is accepted.
const ProtocolVersion = "2025-06-18"

// EffectMetaKey is where a tool's _meta names its effect: read, reversible or
// reaches_people.
const EffectMetaKey = "com.hey/effect"

// MaxLine bounds one message from the app. An Imbox listing is a few hundred kilobytes
// at most; a line past this is not an answer worth waiting for.
const MaxLine = 16 << 20

// Client is one connection to the app's door, initialized and ready for calls. It sends
// one request at a time and reads its answer before the next.
type Client struct {
	conn   net.Conn
	lines  *bufio.Scanner
	lastID int64

	// Server is what the app said about itself in its initialize answer.
	Server ServerInfo
}

type ServerInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// Tool is one command the app offers now.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
	Meta        map[string]any  `json:"_meta,omitempty"`
}

// Effect is what the tool's _meta says running it does, or "" where it says nothing.
func (t Tool) Effect() string {
	effect, _ := t.Meta[EffectMetaKey].(string)
	return effect
}

// Result is the app's answer to a tools/call.
type Result struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Pending is a call the app took but did not carry out: it waits in the app's window for
// the person, who sees Snapshot and sends it or not.
type Pending struct {
	Confirmation json.RawMessage `json:"confirmation,omitempty"`
	Snapshot     json.RawMessage `json:"snapshot,omitempty"`
}

// Dial connects to the door at path and initializes the session. ctx bounds the dial and
// the initialize; each later call is bounded by its own.
func Dial(ctx context.Context, path string) (*Client, error) {
	// Asked first, since a path too long for a socket's address fails the dial with
	// EINVAL whether or not there is anything there.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, &DoorClosedError{Path: path, Cause: err}
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, &DoorClosedError{Path: path, Cause: err}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, &TimeoutError{Method: "connect", Cause: ctxErr}
		}
		return nil, &ConnectError{Path: path, Cause: err}
	}

	client := newClient(conn)
	if err := client.initialize(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// Tools lists every tool the app offers now, following pagination should the app page.
func (c *Client) Tools(ctx context.Context) ([]Tool, error) {
	var tools []Tool
	cursor := ""
	for range 100 {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor,omitempty"`
		}
		if err := c.request(ctx, "tools/list", params, &page); err != nil {
			return nil, err
		}
		// An empty list is the app with nobody signed in; no list at all is no answer.
		if page.Tools == nil {
			return nil, &ProtocolError{Method: "tools/list", Reason: "the result lists no tools"}
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" {
			return tools, nil
		}
		cursor = page.NextCursor
	}
	return nil, &ProtocolError{Reason: "tools/list was still paging after 100 pages"}
}

// Call runs one tool. A refusal comes back as a Result with IsError set, as MCP has it;
// only a failure of the exchange itself is an error.
func (c *Client) Call(ctx context.Context, name string, arguments map[string]any) (*Result, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	var result Result
	if err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments}, &result); err != nil {
		return nil, err
	}
	// MCP's result always carries content. One that does not is no answer, unless it says
	// it is pending, which is never read as anything less.
	if _, pending := result.Pending(); result.Content == nil && !pending {
		return nil, &ProtocolError{Method: "tools/call", Reason: "the result carries no content"}
	}
	return &result, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

// Text is the result's text content, every text block joined by a newline.
func (r *Result) Text() string {
	text := ""
	for _, content := range r.Content {
		if content.Type != "text" {
			continue
		}
		if text != "" {
			text += "\n"
		}
		text += content.Text
	}
	return text
}

// Answer is the result's structured answer: structuredContent, or the text content where
// that is JSON and there is no structuredContent. nil where the result carries neither.
func (r *Result) Answer() json.RawMessage {
	if len(r.StructuredContent) > 0 && string(r.StructuredContent) != "null" {
		return r.StructuredContent
	}
	if text := r.Text(); json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	return nil
}

// Pending reports a call the app answered "pending, waiting in HEY": a structured answer
// whose status is pending, however the rest of it reads, since reading a waiting send as
// done is the one mistake this must not make. A refusal is never pending.
func (r *Result) Pending() (*Pending, bool) {
	answer := r.Answer()
	if r.IsError || answer == nil {
		return nil, false
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(answer, &status); err != nil || status.Status != "pending" {
		return nil, false
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(answer, &fields)
	return &Pending{Confirmation: fields["confirmation"], Snapshot: fields["snapshot"]}, true
}

func newClient(conn net.Conn) *Client {
	lines := bufio.NewScanner(conn)
	lines.Buffer(make([]byte, 0, 64<<10), MaxLine)
	return &Client{conn: conn, lines: lines}
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "hey-cli", "title": "hey local", "version": version.Version},
	}
	var answer struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      ServerInfo `json:"serverInfo"`
	}
	if err := c.request(ctx, "initialize", params, &answer); err != nil {
		return err
	}
	if answer.ProtocolVersion == "" {
		return &ProtocolError{Reason: "initialize answered with no protocolVersion"}
	}
	c.Server = answer.ServerInfo
	if err := c.notify("notifications/initialized"); err != nil {
		return c.exchangeError(ctx, "initialize", err)
	}
	return nil
}

func (c *Client) notify(method string) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

// request sends one request and reads lines until its answer, answering what the app asks
// of the client on the way (a ping) and passing over its notifications.
func (c *Client) request(ctx context.Context, method string, params any, into any) error {
	defer c.bound(ctx)()
	c.lastID++
	id := c.lastID
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return c.exchangeError(ctx, method, err)
	}

	for {
		message, err := c.receive(ctx, method)
		if err != nil {
			return err
		}
		if message.Method != "" {
			if err := c.answerServer(message); err != nil {
				return c.exchangeError(ctx, method, err)
			}
			continue
		}
		if message.JSONRPC != "2.0" {
			return &ProtocolError{Method: method, Reason: "the answer is not JSON-RPC 2.0"}
		}
		if message.Error != nil && isNull(message.ID) {
			// The app could not read the request at all, so it could not name it.
			return &ServerError{Method: method, Code: message.Error.Code, Message: message.Error.Message}
		}
		if string(message.ID) != strconv.FormatInt(id, 10) {
			return &ProtocolError{Method: method, Reason: fmt.Sprintf("the answer is to request %s, not %d", message.ID, id)}
		}
		if message.Error != nil {
			return &ServerError{Method: method, Code: message.Error.Code, Message: message.Error.Message}
		}
		if isNull(message.Result) {
			return &ProtocolError{Method: method, Reason: "the answer has neither a result nor an error"}
		}
		if err := json.Unmarshal(message.Result, into); err != nil {
			return &ProtocolError{Method: method, Reason: "the result does not read as " + method + "'s: " + err.Error()}
		}
		return nil
	}
}

// bound holds the connection to ctx for one exchange: its deadline, or none, and its
// cancellation, which ends whatever read or write waits rather than leave the caller on a
// connection the app holds open. The returned func lets go of ctx.
func (c *Client) bound(ctx context.Context) func() {
	deadline, _ := ctx.Deadline()
	_ = c.conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.conn.SetDeadline(time.Unix(1, 0)) })
	return func() { stop() }
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) send(value any) error {
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(line, '\n'))
	return err
}

func (c *Client) receive(ctx context.Context, method string) (message, error) {
	if !c.lines.Scan() {
		err := c.lines.Err()
		if err == nil {
			return message{}, &ProtocolError{Method: method, Reason: "the app closed the connection without answering", Closed: true}
		}
		if errors.Is(err, bufio.ErrTooLong) {
			return message{}, &ProtocolError{Method: method, Reason: fmt.Sprintf("the answer is longer than %d bytes", MaxLine)}
		}
		return message{}, c.exchangeError(ctx, method, err)
	}
	var received message
	if err := json.Unmarshal(c.lines.Bytes(), &received); err != nil {
		return message{}, &ProtocolError{Method: method, Reason: "the answer is not JSON: " + err.Error()}
	}
	return received, nil
}

// answerServer answers a request the app makes of the client. A ping is the one a client
// with no capabilities is asked; anything else is a method it does not have.
func (c *Client) answerServer(request message) error {
	if isNull(request.ID) {
		return nil
	}
	if request.Method == "ping" {
		return c.send(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
	}
	return c.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      request.ID,
		"error":   map[string]any{"code": -32601, "message": "hey local does not answer " + request.Method},
	})
}

// exchangeError names a failed read or write: the deadline, a cancelled context, or the
// connection itself.
func (c *Client) exchangeError(ctx context.Context, method string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return &TimeoutError{Method: method, Cause: ctxErr}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &TimeoutError{Method: method, Cause: err}
	}
	// On Linux a close with the request unread reaches the client as a reset, not an end.
	if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return &ProtocolError{Method: method, Reason: "the app closed the connection without answering", Closed: true}
	}
	return &ConnectError{Path: c.conn.RemoteAddr().String(), Cause: err}
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}
