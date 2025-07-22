package daemon

import (
	"context"
	"fmt"
	"goweb/go/commands/daemon/daemon_manager"
	"net/http"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/urfave/cli/v3"
)

var manager *daemon_manager.DaemonManager

var Command = &cli.Command{
	Name:  "daemon",
	Usage: "manually manage the daemon process",
	Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		var err error
		manager, err = daemon_manager.FromContext(ctx)
		if err != nil {
			return ctx, fmt.Errorf("failed to get daemon manager: %w", err)
		}
		return ctx, nil
	},
	Commands: []*cli.Command{
		{
			Name:  "start",
			Usage: "start the daemon as a background process",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return manager.Start(ctx)
			},
		},
		{
			Name:  "status",
			Usage: "check the status of the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return manager.Status(ctx)
			},
		},
		{
			Name:  "run",
			Usage: "run the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {

				// router
				mux := http.NewServeMux()
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte("Hello World\n"))
				})

				// server
				var srv *xhttp.Server
				var err error
				srv, err = xhttp.NewServer(&xhttp.ServerConfig{
					Addr:    ":8080",
					Handler: mux,
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
			},
		},
		{
			Name:  "restart",
			Usage: "restart the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return manager.Restart(ctx)
			},
		},
		{
			Name:  "stop",
			Usage: "stop the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return manager.Stop(ctx)
			},
		},
	},
}
