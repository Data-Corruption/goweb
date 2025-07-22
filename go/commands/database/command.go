package database

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var Command = &cli.Command{
	Name:    "database",
	Aliases: []string{"db"},
	Usage:   "Database commands",
	Commands: []*cli.Command{
		{
			Name:    "print",
			Aliases: []string{"p"},
			Usage:   "Print the database contents",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				fmt.Println("work in progress") // TODO: implement
				return nil
			},
		},
	},
}
