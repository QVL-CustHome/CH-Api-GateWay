package server

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestNewServerTimeouts(t *testing.T) {
	backendTimeout := 5 * time.Second
	srv := New(":8080", http.NewServeMux(), backendTimeout)

	if srv.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", srv.Addr)
	}
	if srv.ReadHeaderTimeout != ReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, ReadHeaderTimeout)
	}
	if srv.ReadTimeout != ReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", srv.ReadTimeout, ReadTimeout)
	}
	if srv.IdleTimeout != IdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", srv.IdleTimeout, IdleTimeout)
	}
	if want := backendTimeout + WriteTimeoutGrace; srv.WriteTimeout != want {
		t.Errorf("WriteTimeout = %v, want %v", srv.WriteTimeout, want)
	}
}

func TestRunGracefulShutdown(t *testing.T) {
	srv := New("127.0.0.1:0", http.NewServeMux(), time.Second)
	ctx, cancel := context.WithCancel(context.Background())

	shutdownCalled := false
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, srv, 2*time.Second, func() { shutdownCalled = true })
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() = %v, want nil après un arrêt propre", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() ne s'est pas arrêté après l'annulation du contexte")
	}

	if !shutdownCalled {
		t.Error("le hook onShutdown n'a pas été appelé")
	}
}

func TestRunListenError(t *testing.T) {
	srv := New("127.0.0.1:99999", http.NewServeMux(), time.Second)

	err := Run(context.Background(), srv, time.Second)
	if err == nil {
		t.Fatal("Run() devrait retourner l'erreur d'écoute")
	}
}
