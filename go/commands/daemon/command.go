package daemon

import (
	"context"
	"fmt"
	"goweb/go/commands/daemon/daemon_manager"

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
				// TODO: Implement later
				fmt.Println("wip")
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
