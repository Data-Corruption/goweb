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
				if err := manager.Start(ctx); err != nil {
					return err
				}
				fmt.Println("Daemon started successfully.")
				return nil
			},
		},
		{
			Name:  "status",
			Usage: "check the status of the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				status, err := manager.Status(ctx)
				if err != nil {
					return err
				}
				fmt.Println("Daemon status:", status)
				return nil
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
				if err := manager.Restart(ctx); err != nil {
					return err
				}
				fmt.Println("Daemon restarted successfully.")
				return nil
			},
		},
		{
			Name:  "stop",
			Usage: "stop the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := manager.Stop(ctx); err != nil {
					return err
				}
				fmt.Println("Daemon stopped successfully.")
				return nil
			},
		},
		{
			Name:  "kill",
			Usage: "kill the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := manager.Kill(ctx); err != nil {
					return err
				}
				fmt.Println("Daemon killed successfully.")
				return nil
			},
		},
	},
}
