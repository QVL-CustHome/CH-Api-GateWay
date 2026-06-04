package server

import (
	"context"
	"net/http"
	"time"
)

const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 15 * time.Second
	IdleTimeout       = 60 * time.Second
	WriteTimeoutGrace = 10 * time.Second
)

func New(addr string, handler http.Handler, backendTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      backendTimeout + WriteTimeoutGrace,
		IdleTimeout:       IdleTimeout,
	}
}

func Run(ctx context.Context, srv *http.Server, shutdownGrace time.Duration, onShutdown ...func()) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)

	for _, f := range onShutdown {
		f()
	}
	return err
}
