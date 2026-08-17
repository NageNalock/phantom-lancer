package codexclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// jsonRPCRequest / jsonRPCMessage model the subset of the JSON-RPC 2.0 protocol
// used by `codex app-server`. Per the upstream protocol (openai/codex
// codex-app-server), the transport is newline-delimited JSON over stdio and the
// "jsonrpc":"2.0" header is omitted on the wire, so we do not emit it.
type jsonRPCRequest struct {
	ID     int64  `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// jsonRPCResult is a client->server response to a server-initiated request. The
// id is echoed verbatim as raw JSON because the upstream RequestId is
// `anyOf [string, integer]` and must be returned with its original type.
type jsonRPCResult struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result"`
}

// jsonRPCMessage is any inbound line. It can be:
//   - a response to our request: has id, no method.
//   - a server-initiated request: has both id and method (client must reply).
//   - a notification: has method, no id.
//
// id is captured as raw JSON since the upstream RequestId can be a string or an
// integer; we only parse it to int64 when matching our own outbound requests.
type jsonRPCMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *jsonRPCError   `json:"error,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e jsonRPCError) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

// Notification is a server-initiated JSON-RPC message without an id. The Service
// maps these to Phantom Lancer stable events.
type Notification struct {
	Method string
	Params json.RawMessage
}

// ServerRequest is a server-initiated JSON-RPC request (has both id and method).
// The client must reply via Respond using the same id. Approvals
// (item/commandExecution/requestApproval, item/fileChange/requestApproval)
// arrive as ServerRequests. ID is the raw JSON id (string or integer) and must
// be echoed back verbatim.
type ServerRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// AppServerClient manages a `codex app-server` child process over stdio.
type AppServerClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	mu        sync.Mutex
	nextID    int64
	pending   map[int64]chan jsonRPCMessage
	notifyCh  chan Notification
	requestCh chan ServerRequest
	closed    bool
	closeOnce sync.Once
	closeCh   chan struct{}
	exitErr   error
	doneCh    chan struct{}
}

// StartAppServer launches `codex app-server --listen stdio://` with an
// allowlisted environment and returns a connected client.
func StartAppServer(ctx context.Context, binary, codexHome string) (*AppServerClient, error) {
	cmd := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	cmd.Env = BuildChildEnv(codexHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Drop stderr to avoid leaking secrets into service logs; only summaries are
	// surfaced via probe failures.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	client := &AppServerClient{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    bufio.NewReaderSize(stdout, 1<<20),
		pending:   make(map[int64]chan jsonRPCMessage),
		notifyCh:  make(chan Notification, 64),
		requestCh: make(chan ServerRequest, 32),
		closeCh:   make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
	go client.readLoop()
	return client, nil
}

// PID returns the underlying process id, or 0 if not running.
func (c *AppServerClient) PID() int {
	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// Notifications returns the channel of server-initiated notifications.
func (c *AppServerClient) Notifications() <-chan Notification { return c.notifyCh }

// Requests returns the channel of server-initiated requests that require a
// client response (for example approval requests).
func (c *AppServerClient) Requests() <-chan ServerRequest { return c.requestCh }

// Done is closed when the read loop exits (process gone or transport closed).
func (c *AppServerClient) Done() <-chan struct{} { return c.doneCh }

func (c *AppServerClient) readLoop() {
	defer func() {
		close(c.notifyCh)
		close(c.requestCh)
		close(c.doneCh)
	}()
	for {
		line, err := c.stdout.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			c.failPending(err)
			return
		}
	}
}

func (c *AppServerClient) dispatch(line []byte) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case hasID && msg.Method != "":
		// Server-initiated request: client must reply with the same id.
		select {
		case c.requestCh <- ServerRequest{ID: msg.ID, Method: msg.Method, Params: msg.Params}:
		case <-c.closeCh:
		}
	case hasID:
		// Response to one of our requests. Our outbound ids are always integers.
		var id int64
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if ok {
			ch <- msg
		}
	case msg.Method != "":
		// Notification.
		select {
		case c.notifyCh <- Notification{Method: msg.Method, Params: msg.Params}:
		case <-c.closeCh:
		}
	}
}

func (c *AppServerClient) failPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exitErr = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

func (c *AppServerClient) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Call performs a JSON-RPC request/response round trip with a timeout.
func (c *AppServerClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("app-server client closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan jsonRPCMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	req := jsonRPCRequest{ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		c.removePending(id)
		return nil, err
	}
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		c.removePending(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	case <-c.closeCh:
		c.removePending(id)
		return nil, errors.New("app-server client closed")
	case resp, ok := <-ch:
		if !ok {
			return nil, errors.New("app-server connection lost")
		}
		if resp.Error != nil {
			return nil, *resp.Error
		}
		return resp.Result, nil
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (c *AppServerClient) Notify(method string, params any) error {
	req := jsonRPCRequest{Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("app-server client closed")
	}
	_, err = c.stdin.Write(data)
	return err
}

// Respond replies to a server-initiated request using its raw id (echoed
// verbatim). result is the JSON-RPC result body (for approvals this is the
// decision payload).
func (c *AppServerClient) Respond(id json.RawMessage, result any) error {
	data, err := json.Marshal(jsonRPCResult{ID: id, Result: result})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("app-server client closed")
	}
	_, err = c.stdin.Write(data)
	return err
}

// Initialize performs the initialize/initialized handshake.
func (c *AppServerClient) Initialize(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := c.Call(cctx, "initialize", map[string]any{
		"clientInfo": map[string]any{"name": "phantom-lancer", "version": "1"},
	})
	if err != nil {
		return err
	}
	return c.Notify("initialized", map[string]any{})
}

// Close terminates the child process and releases resources.
func (c *AppServerClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.closeOnce.Do(func() { close(c.closeCh) })
	c.mu.Unlock()
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	return nil
}
