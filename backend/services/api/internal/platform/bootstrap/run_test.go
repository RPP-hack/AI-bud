package bootstrap

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeStopsWorkerWhenListenFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stopped := make(chan struct{})
	srv := &http.Server{Addr: listener.Addr().String()}
	err = Serve(context.Background(), srv, func(ctx context.Context) { <-ctx.Done(); close(stopped) })
	if err == nil {
		t.Fatal("expected occupied port")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("worker not joined")
	}
}

func TestServeShutsDownWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &http.Server{Addr: "127.0.0.1:0"}
	started := make(chan struct{})
	stopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, srv, func(ctx context.Context) { close(started); <-ctx.Done(); close(stopped) })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("worker not stopped")
	}
}

func TestServeWithoutBackground(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Serve(ctx, &http.Server{Addr: "127.0.0.1:0"}, nil); err != nil {
		t.Fatal(err)
	}
}
