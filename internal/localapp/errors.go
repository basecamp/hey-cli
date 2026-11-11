package localapp

import "fmt"

// DoorClosedError is a socket that is not there, or that nothing answers on: the door is
// off in the app's Settings, or the app is not running.
type DoorClosedError struct {
	Path  string
	Cause error
}

func (e *DoorClosedError) Error() string {
	return fmt.Sprintf("nothing answers at %s: %v", e.Path, e.Cause)
}

func (e *DoorClosedError) Unwrap() error { return e.Cause }

// ConnectError is a socket that is there but could not be used: refused by permissions,
// not a socket, or a connection that broke.
type ConnectError struct {
	Path  string
	Cause error
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("could not talk to HEY at %s: %v", e.Path, e.Cause)
}

func (e *ConnectError) Unwrap() error { return e.Cause }

// ServerError is a JSON-RPC error the app answered with: a method or a tool it does not
// have, or arguments it could not read.
type ServerError struct {
	Method  string
	Code    int
	Message string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("HEY refused %s: %s (%d)", e.Method, e.Message, e.Code)
}

// ProtocolError is an answer the client cannot read as MCP. Closed is a connection the
// app closed before it answered, as it does a caller of another user account.
type ProtocolError struct {
	Method string
	Reason string
	Closed bool
}

func (e *ProtocolError) Error() string {
	if e.Method == "" {
		return "HEY's answer could not be read: " + e.Reason
	}
	return fmt.Sprintf("HEY's answer to %s could not be read: %s", e.Method, e.Reason)
}

// TimeoutError is an answer that did not come in time.
type TimeoutError struct {
	Method string
	Cause  error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("HEY did not answer %s in time: %v", e.Method, e.Cause)
}

func (e *TimeoutError) Unwrap() error { return e.Cause }
