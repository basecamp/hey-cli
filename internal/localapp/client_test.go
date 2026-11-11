package localapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/localapp"
	"github.com/basecamp/hey-cli/internal/localapp/localapptest"
)

func dial(t *testing.T, path string) *localapp.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	client, err := localapp.Dial(ctx, path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestDialInitializesBeforeAnythingElse(t *testing.T) {
	door := localapptest.Start(t)
	client := dial(t, door.Path)

	if _, err := client.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests := door.Requests()
	methods := make([]string, 0, len(requests))
	for _, request := range requests {
		methods = append(methods, request.Method)
	}
	if got := strings.Join(methods, " "); got != "initialize notifications/initialized tools/list" {
		t.Errorf("methods = %s", got)
	}
	var initialize struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	_ = json.Unmarshal(door.Requests()[0].Params, &initialize)
	if initialize.ProtocolVersion != localapp.ProtocolVersion {
		t.Errorf("protocolVersion = %q", initialize.ProtocolVersion)
	}
	// MCP requires both of clientInfo's name and version.
	if initialize.ClientInfo.Name != "hey-cli" || initialize.ClientInfo.Version == "" {
		t.Errorf("clientInfo = %+v", initialize.ClientInfo)
	}
	if client.Server.Name != "hey" {
		t.Errorf("server = %+v", client.Server)
	}
}

func TestToolsListsWhatTheAppOffersWithItsEffect(t *testing.T) {
	door := localapptest.Start(t)
	door.Offer("imbox.list", "Lists the Imbox", "read")
	door.Offer("message.send", "Sends a saved draft once you confirm it", "reaches_people")

	tools, err := dial(t, door.Path).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "imbox.list" || tools[1].Name != "message.send" {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[0].Effect() != "read" || tools[1].Effect() != "reaches_people" {
		t.Errorf("effects = %q, %q", tools[0].Effect(), tools[1].Effect())
	}
}

func TestCallSendsTheArgumentsAndReadsTheAnswer(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("screener.decide", map[string]any{"sender": 42, "screened": "out"})

	result, err := dial(t, door.Path).Call(context.Background(), "screener.decide", map[string]any{"sender": 42, "decision": "out"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("refused: %s", result.Text())
	}
	if string(result.Answer()) != `{"screened":"out","sender":42}` {
		t.Errorf("answer = %s", result.Answer())
	}
	if _, pending := result.Pending(); pending {
		t.Error("a decision is not pending")
	}

	calls := door.Calls()
	if len(calls) != 1 || string(calls[0]) != `{"arguments":{"decision":"out","sender":42},"name":"screener.decide"}` {
		t.Errorf("calls = %s", calls)
	}
}

func TestCallWithNoArgumentsSendsAnEmptyObject(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("imbox.list", map[string]any{"threads": []any{}, "has_more": false})

	if _, err := dial(t, door.Path).Call(context.Background(), "imbox.list", nil); err != nil {
		t.Fatal(err)
	}
	if calls := door.Calls(); string(calls[0]) != `{"arguments":{},"name":"imbox.list"}` {
		t.Errorf("calls = %s", calls)
	}
}

func TestASendWaitingInHEYIsPending(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("message.send", map[string]any{
		"status":       "pending",
		"confirmation": 7,
		"snapshot":     map[string]any{"draft": 99, "to": []string{"marcus@example.com"}, "subject": "Stint plan"},
	})

	result, err := dial(t, door.Path).Call(context.Background(), "message.send", map[string]any{"draft": 99})
	if err != nil {
		t.Fatal(err)
	}
	pending, ok := result.Pending()
	if !ok {
		t.Fatalf("not pending: %s", result.Answer())
	}
	if string(pending.Confirmation) != "7" || !strings.Contains(string(pending.Snapshot), "marcus@example.com") {
		t.Errorf("pending = %+v", pending)
	}
}

// Pending is the status alone: a confirmation in a shape this client does not expect
// still waits for the person, and must not read as done.
func TestAPendingAnswerOfAnotherShapeIsStillPending(t *testing.T) {
	door := localapptest.Start(t)
	door.Answer("message.send", map[string]any{"status": "pending", "confirmation": "c-7"})

	result, err := dial(t, door.Path).Call(context.Background(), "message.send", map[string]any{"draft": 99})
	if err != nil {
		t.Fatal(err)
	}
	pending, ok := result.Pending()
	if !ok || string(pending.Confirmation) != `"c-7"` {
		t.Errorf("pending = %+v, %v", pending, ok)
	}
}

func TestARefusalIsNeverPending(t *testing.T) {
	door := localapptest.Start(t)
	door.Refuse("message.send", map[string]any{"status": "pending", "error": "not_offered"})

	result, err := dial(t, door.Path).Call(context.Background(), "message.send", map[string]any{"draft": 99})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("the refusal reads as done")
	}
	if _, pending := result.Pending(); pending {
		t.Error("a refusal reads as pending")
	}
}

func TestNoSocketIsADoorClosed(t *testing.T) {
	_, err := localapp.Dial(context.Background(), filepath.Join(t.TempDir(), "agent.sock"))

	var closed *localapp.DoorClosedError
	if !errors.As(err, &closed) {
		t.Fatalf("err = %v (%T)", err, err)
	}
}

// A crashed app leaves its socket file behind with nothing listening on it, which is as
// closed as no file at all.
func TestASocketNothingAnswersOnIsADoorClosed(t *testing.T) {
	path, listener := localapptest.Listen(t)
	listener.SetUnlinkOnClose(false)
	_ = listener.Close()

	_, err := localapp.Dial(context.Background(), path)

	var closed *localapp.DoorClosedError
	if !errors.As(err, &closed) {
		t.Fatalf("err = %v (%T)", err, err)
	}
}

func TestAJSONRPCErrorIsAServerError(t *testing.T) {
	door := localapptest.Start(t)

	_, err := dial(t, door.Path).Call(context.Background(), "drafts.empty", nil)

	var server *localapp.ServerError
	if !errors.As(err, &server) {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if server.Code != -32602 || server.Method != "tools/call" || !strings.Contains(server.Message, "drafts.empty") {
		t.Errorf("server error = %+v", server)
	}
}

func TestAMalformedAnswerIsAProtocolError(t *testing.T) {
	for name, line := range map[string]string{
		"not JSON":         `{"jsonrpc":"2.0","id":2,"result":`,
		"not JSON-RPC 2.0": `{"jsonrpc":"1.0","id":2,"result":{"tools":[]}}`,
		"another request":  `{"jsonrpc":"2.0","id":41,"result":{"tools":[]}}`,
		"no result":        `{"jsonrpc":"2.0","id":2}`,
		"the wrong shape":  `{"jsonrpc":"2.0","id":2,"result":{"tools":"imbox.list"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			door := localapptest.Start(t)
			door.Reply("tools/list", line)

			_, err := dial(t, door.Path).Tools(context.Background())

			var protocol *localapp.ProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("err = %v (%T)", err, err)
			}
		})
	}
}

func TestAnInitializeWithNoVersionIsAProtocolError(t *testing.T) {
	door := localapptest.Start(t)
	door.Reply("initialize", `{"jsonrpc":"2.0","id":1,"result":{}}`)

	_, err := localapp.Dial(context.Background(), door.Path)

	var protocol *localapp.ProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("err = %v (%T)", err, err)
	}
}

// The app may ask the client something, or tell it something, before it answers; the
// client answers a ping, passes over a notification and still reads its own answer.
func TestTheAppsOwnMessagesBeforeAnAnswerArePassedOver(t *testing.T) {
	path, listener := localapptest.Listen(t)
	answered := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// Held open until the test ends, as the app holds it, so the client's
		// notifications/initialized never meets a closed socket.
		t.Cleanup(func() { _ = conn.Close() })
		buffer := make([]byte, 4096)
		_, _ = conn.Read(buffer) // initialize
		_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}` + "\n"))
		_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","id":"s1","method":"ping"}` + "\n"))
		_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","serverInfo":{"name":"hey"}}}` + "\n"))
		n, _ := conn.Read(buffer)
		answered <- string(buffer[:n])
	}()

	client := dial(t, path)
	if client.Server.Name != "hey" {
		t.Errorf("server = %+v", client.Server)
	}
	select {
	case got := <-answered:
		if !strings.Contains(got, `"id":"s1","jsonrpc":"2.0","result":{}`) {
			t.Errorf("the ping was answered with %s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the ping went unanswered")
	}
}

func TestAnAppThatNeverAnswersTimesOut(t *testing.T) {
	path, listener := localapptest.Listen(t)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := localapp.Dial(ctx, path)

	var timeout *localapp.TimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("err = %v (%T)", err, err)
	}
}

func TestTheSocketIsTheFlagsThenTheEnvironmentsThenTheRuntimeFolders(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv(localapp.SocketEnv, "")
	if path, _ := localapp.SocketPath(""); path != "/run/user/1000/hey/agent.sock" {
		t.Errorf("default = %q", path)
	}
	t.Setenv(localapp.SocketEnv, "/tmp/elsewhere.sock")
	if path, _ := localapp.SocketPath(""); path != "/tmp/elsewhere.sock" {
		t.Errorf("environment = %q", path)
	}
	if path, _ := localapp.SocketPath("/tmp/flag.sock"); path != "/tmp/flag.sock" {
		t.Errorf("flag = %q", path)
	}
}

func TestNoRuntimeFolderIsNoGuess(t *testing.T) {
	t.Setenv(localapp.SocketEnv, "")
	for _, runtime := range []string{"", "run/user/1000"} {
		t.Setenv("XDG_RUNTIME_DIR", runtime)
		if _, err := localapp.SocketPath(""); !errors.Is(err, localapp.ErrNoRuntimeDir) {
			t.Errorf("XDG_RUNTIME_DIR=%q: err = %v", runtime, err)
		}
	}
}

func TestAResultWithNoListOrNoContentIsAProtocolError(t *testing.T) {
	door := localapptest.Start(t)
	door.Reply("tools/list", `{"jsonrpc":"2.0","id":2,"result":{}}`)
	if _, err := dial(t, door.Path).Tools(context.Background()); !errors.As(err, new(*localapp.ProtocolError)) {
		t.Errorf("tools/list with no tools: err = %v (%T)", err, err)
	}

	door = localapptest.Start(t)
	door.AnswerRaw("imbox.list", map[string]any{})
	if _, err := dial(t, door.Path).Call(context.Background(), "imbox.list", nil); !errors.As(err, new(*localapp.ProtocolError)) {
		t.Errorf("tools/call with no content: err = %v (%T)", err, err)
	}
}

// A pending answer is never read as less, whatever else it lacks.
func TestAPendingAnswerWithNoContentIsStillPending(t *testing.T) {
	door := localapptest.Start(t)
	door.AnswerRaw("message.send", map[string]any{"structuredContent": map[string]any{"status": "pending", "confirmation": 7}})

	result, err := dial(t, door.Path).Call(context.Background(), "message.send", map[string]any{"draft": 99})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Pending(); !ok {
		t.Errorf("not pending: %+v", result)
	}
}

// Each call is bounded by its own context, not by the one the dial was given, which here
// has no deadline at all.
func TestACallThatGoesUnansweredTimesOutOnItsOwnContext(t *testing.T) {
	door := localapptest.Start(t)
	door.Hang("tools/call")
	client, err := localapp.Dial(context.Background(), door.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	answered := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := client.Call(ctx, "imbox.list", nil)
		answered <- err
	}()

	select {
	case err := <-answered:
		if !errors.As(err, new(*localapp.TimeoutError)) {
			t.Fatalf("err = %v (%T)", err, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the call outlived its own context")
	}
}

func TestADialWhoseContextEndedIsATimeoutNotAConnectError(t *testing.T) {
	door := localapptest.Start(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := localapp.Dial(ctx, door.Path)

	if !errors.As(err, new(*localapp.TimeoutError)) {
		t.Errorf("err = %v (%T)", err, err)
	}
}
