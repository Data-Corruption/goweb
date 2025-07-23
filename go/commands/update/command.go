package update

import (
	"context"
	"fmt"
	"goweb/go/storage/config"

	"github.com/urfave/cli/v3"
)

var Command = &cli.Command{
	Name:  "update",
	Usage: "update the application or manage update settings",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		// get current version from context
		version, ok := ctx.Value("appVersion").(string)
		if !ok {
			return fmt.Errorf("failed to get appVersion from context")
		}
		return update(ctx, version)
	},
	Commands: []*cli.Command{
		{
			Name:  "check",
			Usage: "check for updates",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				// get current version from context
				version, ok := ctx.Value("appVersion").(string)
				if !ok {
					return fmt.Errorf("failed to get appVersion from context")
				}

				if updateAvailable, err := Check(ctx, version); err != nil {
					return fmt.Errorf("failed to check for updates: %w", err)
				} else if updateAvailable {
					fmt.Println("Update available!")
				} else {
					fmt.Println("No updates available.")
				}

				return nil
			},
		},
		{
			Name:  "notify",
			Usage: "toggle update notifications",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				// get
				updateNotify, err := config.Get[bool](ctx, "updateNotify")
				if err != nil {
					return fmt.Errorf("failed to get updateNotify from config: %w", err)
				}
				// set
				if err := config.Set(ctx, "updateNotify", !updateNotify); err != nil {
					return fmt.Errorf("failed to set updateNotify in config: %w", err)
				}
				if !updateNotify {
					fmt.Println("Update notifications are now enabled.")
				} else {
					fmt.Println("Update notifications are now disabled.")
				}
				return nil
			},
		},
	},
}
