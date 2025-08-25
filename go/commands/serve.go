package commands

import (
	"context"
	"fmt"
	"goweb/go/server"
	"goweb/go/update"
	"net/http"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/Data-Corruption/stdx/xlog"
	"github.com/urfave/cli/v3"
)

var Serve = &cli.Command{
	Name:  "serve",
	Usage: "starts a basic web server",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		var srv *xhttp.Server

		// hello world handler
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Hello World\n"))
		})
		mux.HandleFunc("/update", func(w http.ResponseWriter, r *http.Request) {
			// daemon update example. add auth ofc, etc
			w.Write([]byte("Starting update...\n"))
			if err := update.Update(ctx, true); err != nil {
				xlog.Errorf(ctx, "/update update start failed: %s", err)
			}
		})

		// create server
		var err error
		srv, err = server.New(ctx, mux)
		if err != nil {
			return fmt.Errorf("failed to create server: %w", err)
		}
		server.IntoContext(ctx, srv)

		// start http server
		if err := srv.Listen(); err != nil {
			return fmt.Errorf("server stopped with error: %w", err)
		} else {
			fmt.Println("server stopped gracefully")
		}

		return nil
	},
}
