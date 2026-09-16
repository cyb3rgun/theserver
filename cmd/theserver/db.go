package main

import (
	"context"
	"fmt"
	"io"

	"github.com/cyb3rgun/theserver/internal/config"
)

// runDB holds the database subcommands; S01 has one, info.
func runDB(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "info" {
		fmt.Fprintf(stderr, "theserver: db needs info\n\n%s", usage)
		return 2
	}
	fs, configPath := newFlagSet("db info", stderr)
	if code := parse(fs, args[1:], stderr); code >= 0 {
		return code
	}
	cfg, err := loadConfig(fs, *configPath)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	return dbInfoCommand(cfg, stdout, stderr)
}

func dbInfoCommand(cfg config.Config, stdout, stderr io.Writer) int {
	if err := printDBInfo(context.Background(), cfg, stdout); err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	return 0
}

// printDBInfo opens the database, reports what it holds and closes it again.
func printDBInfo(ctx context.Context, cfg config.Config, out io.Writer) error {
	db, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	info, err := db.Info(ctx)
	if err != nil {
		return err
	}

	foreignKeys := "off"
	if info.ForeignKeys {
		foreignKeys = "on"
	}
	fmt.Fprintf(out, "database:       %s\n", info.Path)
	fmt.Fprintf(out, "schema version: %d\n", info.SchemaVersion)
	fmt.Fprintf(out, "journal mode:   %s\n", info.JournalMode)
	fmt.Fprintf(out, "foreign keys:   %s\n", foreignKeys)
	fmt.Fprintf(out, "busy timeout:   %d ms\n", info.BusyTimeoutMs)
	fmt.Fprintln(out, "rows:")

	width := 0
	for _, table := range info.Tables {
		width = max(width, len(table.Name))
	}
	for _, table := range info.Tables {
		fmt.Fprintf(out, "  %-*s  %6d\n", width, table.Name, table.Rows)
	}
	return nil
}
