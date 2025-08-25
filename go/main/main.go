package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"goweb/go/commands"
	"goweb/go/database"
	"goweb/go/database/config"
	"goweb/go/database/datapath"
	"goweb/go/update"
	"goweb/go/version"

	"github.com/Data-Corruption/stdx/xlog"
	"github.com/urfave/cli/v3"
)

// Template variables ---------------------------------------------------------

const Name = "goweb" // root command name

// ----------------------------------------------------------------------------

const (
	DefaultLogLevel = "warn"
	DataPath        = "/var/lib/" + Name
)

var Version string // set by build script

func main() { os.Exit(run()) }

func run() int {
	// base context with interrupt/termination handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// insert version for update stuff
	ctx = version.IntoContext(ctx, Version)

	// ensure data dir exists
	if _, err := os.Stat(DataPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "data path does not exist: %s\n", DataPath)
		return 1
	}
	ctx = datapath.IntoContext(ctx, DataPath)

	// get log path
	logPath := filepath.Join(DataPath, "logs")
	if err := os.MkdirAll(logPath, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create log path: %s\n", err)
		return 1
	}

	// init logger
	log, err := xlog.New(logPath, DefaultLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %s\n", err)
		return 1
	}
	ctx = xlog.IntoContext(ctx, log)
	defer log.Close()

	// init database
	db, err := database.New(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize database: %s\n", err)
		return 1
	}
	ctx = database.IntoContext(ctx, db)
	defer db.Close()
	xlog.Debug(ctx, "Database initialized")

	// init config
	ctx, err = config.Init(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize config: %s\n", err)
		return 1
	}
	xlog.Debug(ctx, "Config initialized")

	// set log level
	cfgLogLevel, err := config.Get[string](ctx, "logLevel")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get log level from config: %s\n", err)
		return 1
	}
	if err := log.SetLevel(cfgLogLevel); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set log level: %s\n", err)
		return 1
	}

	// Update check
	updateNotify, err := config.Get[bool](ctx, "updateNotify")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get updateNotify from config: %s\n", err)
		return 1
	}
	if updateNotify {
		// get last update check time from config
		tStr, err := config.Get[string](ctx, "lastUpdateCheck")
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get lastUpdateCheck from config: %s\n", err)
			return 1
		}
		t, err := time.Parse(time.RFC3339, tStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to parse lastUpdateCheck time: %s\n", err)
			return 1
		}

		// once a day, very lightweight check
		if time.Since(t) > 24*time.Hour {
			xlog.Debug(ctx, "Checking for updates...")

			// update check time in config
			if err := config.Set(ctx, "lastUpdateCheck", time.Now().Format(time.RFC3339)); err != nil {
				fmt.Fprintf(os.Stderr, "failed to set lastUpdateCheck in config: %s\n", err)
				return 1
			}

			updateAvailable, err := update.Check(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to check for updates: %s\n", err)
				return 1
			}
			if updateAvailable {
				fmt.Println("Update available! Run 'goweb update check' to see details.")
			}
		}
	}

	// init app
	app := &cli.Command{
		Name:    Name,
		Version: Version,
		Usage:   "example CLI application with web capabilities",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "log",
				Value: DefaultLogLevel,
				Usage: "override log level (debug|info|warn|error|none)",
			},
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "answer yes to all prompts",
			},
		},
		Commands: []*cli.Command{
			commands.Update,
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			logLevel := cmd.String("log")
			if logLevel != DefaultLogLevel {
				if err := log.SetLevel(logLevel); err != nil {
					return ctx, err
				}
			}
			return ctx, nil
		},
	}

	// run app
	if err := app.Run(ctx, os.Args); err != nil {
		log.Error(err)
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
