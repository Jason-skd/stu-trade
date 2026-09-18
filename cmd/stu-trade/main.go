package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Jason-skd/stu-trade/internal/app"
)

var (
	version = "dev"
	commit  = "unknown"
)

// === 启动命令进程 ===

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	// === 解析命令 ===
	command, commandArgs := splitCommand(args)
	if command == "version" {
		if len(commandArgs) != 0 {
			return usageError()
		}
		fmt.Fprintf(stdout, "%s (%s)\n", version, commit)
		return nil
	}
	if command != "serve" && command != "migrate" {
		return usageError()
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	profile := flags.String("profile", "", "deployment profile")
	if command == "migrate" {
		if len(commandArgs) == 0 || commandArgs[0] != "up" {
			return usageError()
		}
		commandArgs = commandArgs[1:]
	}
	if err := flags.Parse(commandArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return usageError()
	}

	// === 加载进程依赖 ===
	cfg, err := app.LoadConfig(app.LoadOptions{Profile: *profile, Version: version, Commit: commit})
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	// === 执行命令 ===
	switch command {
	case "serve":
		return app.Serve(ctx, cfg, logger)
	case "migrate":
		return app.Migrate(ctx, cfg, logger)
	default:
		return usageError()
	}
}

// === 解析命令参数 ===

func splitCommand(args []string) (string, []string) {
	if len(args) == 0 || args[0] == "--" || args[0] == "-" || args[0] == "-h" || args[0] == "--help" || args[0] == "--profile" || strings.HasPrefix(args[0], "--profile=") {
		return "serve", args
	}
	if args[0] == "-v" || args[0] == "--version" {
		return "version", args[1:]
	}
	return args[0], args[1:]
}

func usageError() error {
	return errors.New("usage: stu-trade [serve|migrate up|version] [--profile=develop|native|production]")
}
