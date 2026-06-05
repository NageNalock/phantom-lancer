package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type appServerClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	notify chan appServerNotification

	nextID int64

	mu      sync.Mutex
	pending map[string]chan rpcResult

	writeMu    sync.Mutex
	done       chan struct{}
	once       sync.Once
	notifyOnce sync.Once
}

type appServerNotification struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type rpcResult struct {
	Result json.RawMessage
	Err    error
}

type rpcError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func startAppServer(ctx context.Context, binary string, env []string) (*appServerClient, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, "app-server", "--stdio")
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	client := &appServerClient{
		cmd:     cmd,
		stdin:   stdin,
		notify:  make(chan appServerNotification, 128),
		pending: make(map[string]chan rpcResult),
		done:    make(chan struct{}),
	}
	go client.readStdout(stdout)
	go client.discardStderr(stderr)
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = errors.New("codex app-server 已退出")
		}
		client.closeWithError(err)
	}()

	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var initialized struct {
		UserAgent string `json:"userAgent"`
		CodexHome string `json:"codexHome"`
	}
	if err := client.Call(initCtx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "phantom-lancer",
			"title":   "Phantom Lancer",
			"version": "dev",
		},
		"capabilities": map[string]any{
			"experimentalApi":    true,
			"requestAttestation": false,
		},
	}, &initialized); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func (c *appServerClient) Call(ctx context.Context, method string, params any, out any) error {
	id := "rpc_" + strconv.FormatInt(atomic.AddInt64(&c.nextID, 1), 10)
	ch := make(chan rpcResult, 1)

	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return errors.New("codex app-server 未运行")
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()

	payload := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		payload["params"] = params
	}
	data, err := json.Marshal(payload)
	if err != nil {
		c.dropPending(id)
		return err
	}

	c.writeMu.Lock()
	_, writeErr := c.stdin.Write(append(data, '\n'))
	c.writeMu.Unlock()
	if writeErr != nil {
		c.dropPending(id)
		return writeErr
	}

	select {
	case <-ctx.Done():
		c.dropPending(id)
		return ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return result.Err
		}
		if out == nil {
			return nil
		}
		if len(result.Result) == 0 || string(result.Result) == "null" {
			return nil
		}
		return json.Unmarshal(result.Result, out)
	case <-c.done:
		c.dropPending(id)
		return errors.New("codex app-server 连接已关闭")
	}
}

func (c *appServerClient) Notifications() <-chan appServerNotification {
	return c.notify
}

func (c *appServerClient) Running() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *appServerClient) Close() {
	c.closeWithError(errors.New("codex app-server 已关闭"))
}

func (c *appServerClient) readStdout(stdout io.Reader) {
	defer c.closeNotify()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		if idRaw, ok := raw["id"]; ok {
			c.resolveResponse(idRaw, raw)
			continue
		}
		methodRaw, ok := raw["method"]
		if !ok {
			continue
		}
		var method string
		if err := json.Unmarshal(methodRaw, &method); err != nil || method == "" {
			continue
		}
		params := map[string]any{}
		if paramsRaw, ok := raw["params"]; ok && len(paramsRaw) > 0 {
			_ = json.Unmarshal(paramsRaw, &params)
		}
		notification := appServerNotification{Method: method, Params: params}
		select {
		case <-c.done:
			return
		default:
		}
		select {
		case c.notify <- notification:
		case <-c.done:
			return
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		c.closeWithError(err)
		return
	}
	c.closeWithError(errors.New("codex app-server 输出已关闭"))
}

func (c *appServerClient) discardStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
	}
}

func (c *appServerClient) resolveResponse(idRaw json.RawMessage, raw map[string]json.RawMessage) {
	id := requestIDString(idRaw)
	if id == "" {
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if errorRaw, ok := raw["error"]; ok {
		var rpcErr rpcError
		_ = json.Unmarshal(errorRaw, &rpcErr)
		if rpcErr.Message == "" {
			rpcErr.Message = "Codex app-server 请求失败"
		}
		ch <- rpcResult{Err: fmt.Errorf("%s", rpcErr.Message)}
		return
	}
	ch <- rpcResult{Result: raw["result"]}
}

func (c *appServerClient) dropPending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *appServerClient) closeWithError(err error) {
	c.once.Do(func() {
		close(c.done)
		_ = c.stdin.Close()
		if c.cmd.Process != nil && c.cmd.ProcessState == nil {
			_ = c.cmd.Process.Kill()
		}
		c.mu.Lock()
		for id, ch := range c.pending {
			delete(c.pending, id)
			ch <- rpcResult{Err: err}
		}
		c.mu.Unlock()
	})
}

func (c *appServerClient) closeNotify() {
	c.notifyOnce.Do(func() {
		close(c.notify)
	})
}

func requestIDString(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		return strconv.FormatInt(number, 10)
	}
	return ""
}
