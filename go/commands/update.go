//go:build linux

package commands

import (
	"context"
	"fmt"
	"goweb/go/database/config"
	"goweb/go/update"
	"goweb/go/version"

	"github.com/urfave/cli/v3"
)

var Update = &cli.Command{
	Name:  "update",
	Usage: "update the application or manage update settings",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		version := version.FromContext(ctx)
		if version == "" {
			return fmt.Errorf("failed to get appVersion from context")
		}
		return update.Update(ctx, false)
	},
}

var UpdateToggleNotify = &cli.Command{
	Name:  "update-toggle-notify",
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
}
