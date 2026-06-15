package mail

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"phantom-lancer/internal/mail/imapsync"
)

func TestCloseSignalsBackgroundAndReturns(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	stop := make(chan struct{})
	done := make(chan struct{})
	workerExited := make(chan struct{})

	svc := &Service{
		log:             logger,
		running:         true,
		backgroundStop:  stop,
		backgroundDone:  done,
		imapSyncManager: imapsync.NewManager(nil, logger),
	}

	go func() {
		<-stop
		close(workerExited)
		close(done)
	}()

	returned := make(chan error, 1)
	go func() {
		returned <- svc.Close()
	}()

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close blocked waiting for background worker")
	}

	select {
	case <-workerExited:
	default:
		t.Fatal("Close returned before signaling the background worker")
	}
}
