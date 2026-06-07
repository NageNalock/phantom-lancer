package codexclient

import (
	"encoding/json"
	"testing"
)

// TestDispatchRoutesMessageKinds verifies the client correctly distinguishes
// responses, server-initiated requests and notifications per the upstream
// JSON-RPC framing (id+method = request, id only = response, method only =
// notification).
func TestDispatchRoutesMessageKinds(t *testing.T) {
	c := &AppServerClient{
		pending:   make(map[int64]chan jsonRPCMessage),
		notifyCh:  make(chan Notification, 4),
		requestCh: make(chan ServerRequest, 4),
		doneCh:    make(chan struct{}),
	}

	// Response to a pending request.
	respCh := make(chan jsonRPCMessage, 1)
	c.pending[10] = respCh
	c.dispatch([]byte(`{"id":10,"result":{"ok":true}}`))
	select {
	case msg := <-respCh:
		if string(msg.ID) != "10" {
			t.Fatalf("expected response for id 10, got %+v", msg)
		}
	default:
		t.Fatal("expected response to be routed to pending channel")
	}

	// Server-initiated request (has id and method).
	c.dispatch([]byte(`{"id":20,"method":"item/commandExecution/requestApproval","params":{"itemId":"x"}}`))
	select {
	case req := <-c.requestCh:
		if string(req.ID) != "20" || req.Method != "item/commandExecution/requestApproval" {
			t.Fatalf("unexpected server request: %+v", req)
		}
	default:
		t.Fatal("expected server request to be routed to request channel")
	}

	// Server-initiated request with a STRING id (RequestId is anyOf[string,int]).
	c.dispatch([]byte(`{"id":"abc","method":"item/fileChange/requestApproval","params":{}}`))
	select {
	case req := <-c.requestCh:
		if string(req.ID) != `"abc"` {
			t.Fatalf("expected raw string id preserved, got %s", req.ID)
		}
	default:
		t.Fatal("expected string-id server request to be routed")
	}

	// Notification (method only).
	c.dispatch([]byte(`{"method":"turn/started","params":{}}`))
	select {
	case notif := <-c.notifyCh:
		if notif.Method != "turn/started" {
			t.Fatalf("unexpected notification: %+v", notif)
		}
	default:
		t.Fatal("expected notification to be routed to notify channel")
	}
}

// TestRequestOmitsJSONRPCHeader verifies the wire format omits the
// "jsonrpc":"2.0" header per the upstream protocol.
func TestRequestOmitsJSONRPCHeader(t *testing.T) {
	data, err := json.Marshal(jsonRPCRequest{ID: 1, Method: "thread/start", Params: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	if _, ok := generic["jsonrpc"]; ok {
		t.Fatalf("jsonrpc header must be omitted on the wire, got %s", data)
	}
}
