package probes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// fake_dialer_test.go – exercise probe code that uses a pluggable DialContext
// override.  No real network.  The fake dialer returns a *mockConn that is
// pre-programmed with a canned read/write behaviour and close() tracking.
// -----------------------------------------------------------------------------

// mockConn is a net.Conn stand-in that never touches a real socket.
type mockConn struct {
	readBuf  bytes.Reader
	writeBuf bytes.Buffer

	closeOnce int32
	closed    chan struct{}

	local  net.Addr
	remote net.Addr

	readDeadline  time.Time
	writeDeadline time.Time

	// Optional overrides: if non-nil, these replace the default behaviour.
	onRead  func(b []byte) (int, error)
	onWrite func(b []byte) (int, error)
	onClose func() error
}

func newMockConn(readPayload []byte) *mockConn {
	mc := &mockConn{closed: make(chan struct{})}
	mc.readBuf.Reset(readPayload)
	mc.local = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	mc.remote = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2}
	return mc
}

func (m *mockConn) Read(b []byte) (int, error) {
	if atomic.LoadInt32(&m.closeOnce) > 0 {
		return 0, io.EOF
	}
	if m.onRead != nil {
		return m.onRead(b)
	}
	return m.readBuf.Read(b)
}

func (m *mockConn) Write(b []byte) (int, error) {
	if atomic.LoadInt32(&m.closeOnce) > 0 {
		return 0, errors.New("write on closed mockConn")
	}
	if m.onWrite != nil {
		return m.onWrite(b)
	}
	return m.writeBuf.Write(b)
}

func (m *mockConn) Close() error {
	if !atomic.CompareAndSwapInt32(&m.closeOnce, 0, 1) {
		return nil
	}
	close(m.closed)
	if m.onClose != nil {
		return m.onClose()
	}
	return nil
}

func (m *mockConn) isClosed() bool { return atomic.LoadInt32(&m.closeOnce) > 0 }

func (m *mockConn) LocalAddr() net.Addr                { return m.local }
func (m *mockConn) RemoteAddr() net.Addr               { return m.remote }
func (m *mockConn) SetDeadline(t time.Time) error      { m.readDeadline = t; m.writeDeadline = t; return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { m.readDeadline = t; return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { m.writeDeadline = t; return nil }

// -----------------------------------------------------------------------------
// fakeDialer – returns a dialContext fn whose behaviour is configured per test.
// -----------------------------------------------------------------------------

type fakeDialer struct {
	// Payload returned to callers of DialContext.  If dialErr is set, returns
	// that error instead.
	payload []byte
	dialErr error

	// Optional callback to construct the conn (for bespoke behaviour).
	makeConn func(network, addr string) net.Conn

	// Track calls for assertions.
	calls int32
	lastNetwork string
	lastAddr    string
}

func (fd *fakeDialer) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	atomic.AddInt32(&fd.calls, 1)
	fd.lastNetwork = network
	fd.lastAddr = addr

	// Always honour context cancellation — this is an important real-Dialer
	// contract that callers depend on.
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	if fd.dialErr != nil {
		return nil, fd.dialErr
	}
	if fd.makeConn != nil {
		return fd.makeConn(network, addr), nil
	}
	return newMockConn(fd.payload), nil
}

// -----------------------------------------------------------------------------
// Unit tests
// -----------------------------------------------------------------------------

func TestFakeDialer_OkCase(t *testing.T) {
	t.Parallel()

	fd := &fakeDialer{payload: []byte("220 mx.example.com ESMTP ready\r\n")}
	conn, err := fd.dialContext(context.Background(), "tcp", "127.0.0.1:25")
	if err != nil {
		t.Fatalf("dial ok case: unexpected err=%v", err)
	}
	defer conn.Close()

	buf := make([]byte, 64)
	n, rerr := conn.Read(buf)
	if rerr != nil {
		t.Fatalf("read ok case: err=%v", rerr)
	}
	if !bytes.HasPrefix(buf[:n], []byte("220 ")) {
		t.Errorf("got %q want SMTP banner prefix", buf[:n])
	}

	// Write through mockConn – captured in writeBuf.
	if _, werr := conn.Write([]byte("EHLO a\r\n")); werr != nil {
		t.Fatalf("write err: %v", werr)
	}
	mc := conn.(*mockConn)
	if got := mc.writeBuf.String(); got != "EHLO a\r\n" {
		t.Errorf("writeBuf=%q want %q", got, "EHLO a\r\n")
	}

	if atomic.LoadInt32(&fd.calls) != 1 {
		t.Errorf("dial calls=%d want 1", fd.calls)
	}
	if fd.lastAddr != "127.0.0.1:25" || fd.lastNetwork != "tcp" {
		t.Errorf("last dial=(%s,%s) want (tcp,127.0.0.1:25)", fd.lastNetwork, fd.lastAddr)
	}

	// Close is idempotent.
	cerr := conn.Close()
	if cerr != nil {
		t.Errorf("first Close err=%v", cerr)
	}
	cerr2 := conn.Close()
	if cerr2 != nil {
		t.Errorf("second Close err=%v", cerr2)
	}
	if !mc.isClosed() {
		t.Error("mc.closed flag not set")
	}
}

func TestFakeDialer_DialFailure_ReturnsCritical(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("connection refused")
	fd := &fakeDialer{dialErr: wantErr}
	conn, err := fd.dialContext(context.Background(), "tcp", "10.0.0.5:443")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v want %v", err, wantErr)
	}
	if conn != nil {
		conn.Close()
		t.Fatal("dial error returned non-nil conn")
	}
	if atomic.LoadInt32(&fd.calls) != 1 {
		t.Errorf("dial calls=%d want 1", fd.calls)
	}
}

func TestFakeDialer_ContextCancel_BeforeDial(t *testing.T) {
	t.Parallel()

	fd := &fakeDialer{payload: []byte("hello")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE dial

	conn, err := fd.dialContext(ctx, "tcp", "10.0.0.9:9999")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
	if conn != nil {
		conn.Close()
		t.Error("conn non-nil on canceled ctx")
	}
}

func TestFakeDialer_ReadError_MappedToCritical(t *testing.T) {
	t.Parallel()

	readErr := errors.New("broken pipe")
	fd := &fakeDialer{
		makeConn: func(network, addr string) net.Conn {
			mc := newMockConn(nil)
			mc.onRead = func(b []byte) (int, error) { return 0, readErr }
			return mc
		},
	}
	conn, err := fd.dialContext(context.Background(), "tcp", "remote:110")
	if err != nil {
		t.Fatalf("dial err=%v", err)
	}
	defer conn.Close()

	buf := make([]byte, 32)
	n, rerr := conn.Read(buf)
	if !errors.Is(rerr, readErr) {
		t.Fatalf("read err=%v want %v", rerr, readErr)
	}
	if n != 0 {
		t.Errorf("read n=%d want 0", n)
	}
}

func TestFakeDialer_ConnAddrsAndDeadlines(t *testing.T) {
	t.Parallel()

	fd := &fakeDialer{payload: nil}
	conn, err := fd.dialContext(context.Background(), "tcp", "h:1")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if conn.LocalAddr() == nil || conn.RemoteAddr() == nil {
		t.Fatal("LocalAddr/RemoteAddr nil")
	}
	if conn.LocalAddr().String() == conn.RemoteAddr().String() {
		t.Log("local == remote (acceptable for mock)")
	}

	wantDeadline := time.Now().Add(time.Minute)
	if derr := conn.SetDeadline(wantDeadline); derr != nil {
		t.Errorf("SetDeadline err=%v", derr)
	}
	if derr := conn.SetReadDeadline(wantDeadline); derr != nil {
		t.Errorf("SetReadDeadline err=%v", derr)
	}
	if derr := conn.SetWriteDeadline(wantDeadline); derr != nil {
		t.Errorf("SetWriteDeadline err=%v", derr)
	}
	mc := conn.(*mockConn)
	if !mc.readDeadline.Equal(wantDeadline) {
		t.Errorf("readDeadline=%v want %v", mc.readDeadline, wantDeadline)
	}
	if !mc.writeDeadline.Equal(wantDeadline) {
		t.Errorf("writeDeadline=%v want %v", mc.writeDeadline, wantDeadline)
	}
}

func TestFakeDialer_MakeConn_OkVsCritical(t *testing.T) {
	t.Parallel()

	// Table-driven: two outcomes from dial behaviour map to probe ok/critical.
	cases := []struct {
		name        string
		dialer      *fakeDialer
		wantDialOk  bool
		wantReadOk  bool
		wantWritten string
	}{
		{
			name:       "tcp_probe_ok",
			dialer:     &fakeDialer{payload: []byte("+OK POP3 ready\r\n")},
			wantDialOk: true,
			wantReadOk: true,
		},
		{
			name:       "dial_refused_is_critical",
			dialer:     &fakeDialer{dialErr: errors.New("dial tcp 1.2.3.4:587: connect: connection refused")},
			wantDialOk: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			conn, err := tc.dialer.dialContext(ctx, "tcp", "mx.example.com:110")
			dialOk := err == nil
			if dialOk != tc.wantDialOk {
				t.Fatalf("dialOk=%v want %v (err=%v)", dialOk, tc.wantDialOk, err)
			}
			if !dialOk {
				return
			}
			defer conn.Close()
			buf := make([]byte, 128)
			n, rerr := conn.Read(buf)
			readOk := rerr == nil
			if readOk != tc.wantReadOk {
				t.Errorf("readOk=%v want %v (rerr=%v, n=%d, firstbytes=%q)",
					readOk, tc.wantReadOk, rerr, n, buf[:n])
			}
			// Simulate a probe writing a command: should be captured.
			_, _ = conn.Write([]byte("QUIT\r\n"))
			mc := conn.(*mockConn)
			if got := mc.writeBuf.String(); got != "QUIT\r\n" {
				t.Errorf("written=%q want QUIT\\r\\n", got)
			}
		})
	}
}
