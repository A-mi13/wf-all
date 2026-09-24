package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Serve обслуживает ln до отмены ctx, затем даёт запросам доработать shutdownTimeout.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, shutdownTimeout time.Duration) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			return err
		}
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
