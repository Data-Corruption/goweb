package update

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var Command = &cli.Command{
	Name:  "update",
	Usage: "manually manage the daemon process",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		fmt.Println("hello")
		return nil
	},
}
