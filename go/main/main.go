package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"goweb/go/commands/daemon"
	"goweb/go/commands/daemon/daemon_manager"
	"goweb/go/commands/database"
	"goweb/go/commands/update"
	"goweb/go/storage/config"
	"goweb/go/storage/storagepath"

	"github.com/Data-Corruption/stdx/xlog"
	"github.com/urfave/cli/v3"
)

// used for root command name and default storage path
const name = "goweb"

// Version set by build script
var Version string

var cleanUpFuncs []func() error

func main() {
	defer cleanup()
	app := &cli.Command{
		Name:        name,
		Usage:       "example CLI application with web capabilities",
		Version:     Version,
		Description: name + " is a CLI application that provides web capabilities and various commands to manage the application.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"vb"},
				Usage:   "enable verbose output",
			},
			&cli.StringFlag{
				Name:  "storage",
				Usage: "override storage `DIR`. Default is ~/." + name,
			},
		},
		Commands: []*cli.Command{
			update.Command,
			database.Command,
			daemon.Command,
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			return startup(ctx, cmd)
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Default action when no subcommand is provided, replace this if desired
			if cmd.Bool("verbose") {
				fmt.Println("Verbose mode enabled")
			}
			fmt.Println("No command provided. Use --help or -h for help.")
			return nil
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Println(err)
	}
}

func startup(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	// insert version into context under "appVersion"
	ctx = context.WithValue(ctx, "appVersion", Version)

	// Set storage path
	var err error
	ctx, err = storagepath.Init(ctx, cmd.String("storage"), name)
	if err != nil {
		return ctx, fmt.Errorf("failed to initialize storage path: %w", err)
	}
	storagePath := storagepath.FromContext(ctx)

	// Init Logger
	initLogLevel := "none"
	if cmd.Bool("verbose") {
		initLogLevel = "debug"
	}
	log, err := xlog.New(filepath.Join(storagePath, "logs"), initLogLevel)
	if err != nil {
		return ctx, fmt.Errorf("failed to initialize logger: %w", err)
	}
	ctx = xlog.IntoContext(ctx, log)
	cleanUpFuncs = append(cleanUpFuncs, log.Close)

	xlog.Debugf(ctx, "Starting %s, version: %s, storage path: %s", name, Version, storagePath)

	// Init Database
	db, err := database.New(ctx)
	if err != nil {
		return ctx, fmt.Errorf("failed to initialize database: %w", err)
	}
	ctx = database.IntoContext(ctx, db)
	dbClose := func() error { db.Close(); return nil }
	cleanUpFuncs = append(cleanUpFuncs, dbClose)
	xlog.Debug(ctx, "Database initialized")

	// Init Config
	ctx, err = config.Init(ctx)
	if err != nil {
		return ctx, fmt.Errorf("failed to initialize config: %w", err)
	}
	xlog.Debug(ctx, "Config initialized")

	// Set log level
	if initLogLevel != "debug" {
		cfgLogLevel, err := config.Get[string](ctx, "logLevel")
		if err != nil {
			return ctx, fmt.Errorf("failed to get log level from config: %w", err)
		}
		if err := log.SetLevel(cfgLogLevel); err != nil {
			return ctx, fmt.Errorf("failed to set log level: %w", err)
		}
	}

	// Update check
	updateNotify, err := config.Get[bool](ctx, "updateNotify")
	if err != nil {
		return ctx, fmt.Errorf("failed to get updateNotify from config: %w", err)
	}
	if updateNotify {
		// get last update check time from config
		tStr, err := config.Get[string](ctx, "lastUpdateCheck")
		if err != nil {
			return ctx, fmt.Errorf("failed to get lastUpdateCheck from config: %w", err)
		}
		t, err := time.Parse(time.RFC3339, tStr)
		if err != nil {
			return ctx, fmt.Errorf("failed to parse lastUpdateCheck time: %w", err)
		}

		// once a day, very lightweight check, to be polite to github
		if time.Since(t) > 24*time.Hour {
			xlog.Debug(ctx, "Checking for updates...")

			// update check time in config
			if err := config.Set(ctx, "lastUpdateCheck", time.Now().Format(time.RFC3339)); err != nil {
				return ctx, fmt.Errorf("failed to set lastUpdateCheck in config: %w", err)
			}

			updateAvailable, err := update.Check(ctx, Version)
			if err != nil {
				return ctx, fmt.Errorf("failed to check for updates: %w", err)
			}
			if updateAvailable {
				fmt.Println("Update available! Run 'goweb update check' to see details.")
			}
		}
	}

	// Init daemon manager
	manager := &daemon_manager.DaemonManager{
		PIDFilePath:   filepath.Join(storagePath, "daemon.pid"),
		ReadyTimeout:  10 * time.Second,
		StopTimeout:   10 * time.Second,
		DaemonRunArgs: []string{"daemon", "run"},
	}
	ctx, err = daemon_manager.IntoContext(ctx, manager)
	if err != nil {
		return ctx, fmt.Errorf("failed to insert daemon manager into context: %w", err)
	}
	xlog.Debug(ctx, "Daemon manager initialized")

	// Init other components

	return ctx, nil
}

func cleanup() {
	// call clean up funcs in reverse order
	for i := len(cleanUpFuncs) - 1; i >= 0; i-- {
		if err := cleanUpFuncs[i](); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to clean up: %v\n", err)
		}
	}
}
