package server

import (
	"context"
	"fmt"
	"goweb/go/database/config"
	"goweb/go/database/datapath"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/Data-Corruption/stdx/xlog"
)

type ctxKey struct{}

func IntoContext(ctx context.Context, srv *xhttp.Server) context.Context {
	return context.WithValue(ctx, ctxKey{}, srv)
}

func FromContext(ctx context.Context) *xhttp.Server {
	if srv, ok := ctx.Value(ctxKey{}).(*xhttp.Server); ok {
		return srv
	}
	return nil
}

func New(ctx context.Context, handler http.Handler) (*xhttp.Server, error) {
	// get http server related stuff from config
	port, err := config.Get[int](ctx, "port")
	if err != nil {
		return nil, fmt.Errorf("failed to get port from config: %w", err)
	}
	useTLS, err := config.Get[bool](ctx, "useTLS")
	if err != nil {
		return nil, fmt.Errorf("failed to get useTLS from config: %w", err)
	}
	tlsKeyPath, err := config.Get[string](ctx, "tlsKeyPath")
	if err != nil {
		return nil, fmt.Errorf("failed to get tlsKeyPath from config: %w", err)
	}
	tlsCertPath, err := config.Get[string](ctx, "tlsCertPath")
	if err != nil {
		return nil, fmt.Errorf("failed to get tlsCertPath from config: %w", err)
	}

	// create http server
	var srv *xhttp.Server
	srv, err = xhttp.NewServer(&xhttp.ServerConfig{
		Addr:        fmt.Sprintf(":%d", port),
		UseTLS:      useTLS,
		TLSKeyPath:  tlsKeyPath,
		TLSCertPath: tlsCertPath,
		Handler:     handler,
		AfterListen: func() {
			// write health file
			healthFilePath := filepath.Join(filepath.Dir(datapath.FromContext(ctx)), "health")
			xlog.Debugf(ctx, "writing health file: %s", healthFilePath)
			if err := os.WriteFile(healthFilePath, []byte("ok"), 0644); err != nil {
				xlog.Errorf(ctx, "failed to write health file: %s", err)
			}
			fmt.Printf("Server is listening on http://localhost%s\n", srv.Addr())
		},
		OnShutdown: func() {
			fmt.Println("shutting down, cleaning up resources ...")
		},
	})
	return srv, err
}
