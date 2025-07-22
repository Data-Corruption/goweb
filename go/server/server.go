package server

import (
	"context"
	"fmt"
	"goweb/go/commands/daemon/daemon_manager"
	"goweb/go/storage/config"
	"net/http"

	"github.com/Data-Corruption/stdx/xhttp"
)

func Run(ctx context.Context) error {
	// router
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World\n"))
	})

	// get server related stuff from config
	port, err := config.Get[int](ctx, "port")
	if err != nil {
		return fmt.Errorf("failed to get port from config: %w", err)
	}
	useTLS, err := config.Get[bool](ctx, "useTLS")
	if err != nil {
		return fmt.Errorf("failed to get useTLS from config: %w", err)
	}
	tlsKeyPath, err := config.Get[string](ctx, "tlsKeyPath")
	if err != nil {
		return fmt.Errorf("failed to get tlsKeyPath from config: %w", err)
	}
	tlsCertPath, err := config.Get[string](ctx, "tlsCertPath")
	if err != nil {
		return fmt.Errorf("failed to get tlsCertPath from config: %w", err)
	}

	// server
	var srv *xhttp.Server
	srv, err = xhttp.NewServer(&xhttp.ServerConfig{
		Addr:        fmt.Sprintf(":%d", port),
		UseTLS:      useTLS,
		TLSKeyPath:  tlsKeyPath,
		TLSCertPath: tlsCertPath,
		Handler:     mux,
		AfterListen: func() {
			if err := daemon_manager.NotifyReady(ctx); err != nil {
				fmt.Printf("failed to notify daemon manager: %v\n", err)
			}
			fmt.Printf("server is ready and listening on http://localhost%s\n", srv.Addr())
		},
		OnShutdown: func() {
			fmt.Println("shutting down, cleaning up resources ...")
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Start serving (blocks until exit signal or error).
	if err := srv.Listen(); err != nil {
		return fmt.Errorf("server stopped with error: %w", err)
	} else {
		fmt.Println("server stopped gracefully")
	}

	return nil
}
