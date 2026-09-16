// Command simtarget is a simulated target for testing the device link without
// firmware. It journals its events, connects like a target, replays what the
// server has not acknowledged, and answers commands.
//
//	simtarget --server wss://127.0.0.1:8443 --id tgt-01 --token <token> --insecure
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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cyb3rgun/theserver/internal/simtarget"
	"github.com/cyb3rgun/theserver/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("simtarget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", "wss://127.0.0.1:8443", "theserver address")
	id := fs.String("id", "", "device id, as registered with theserver device add")
	token := fs.String("token", "", "device token printed by theserver device add")
	insecure := fs.Bool("insecure", false, "skip TLS verification (test servers only)")
	rate := fs.Float64("rate", 5, "events per second")
	controllers := fs.Int("controllers", 3, "number of controller ids to rotate")
	journalDir := fs.String("journal", "", "directory for the persisted journal (default data/simtarget/<id>)")
	dropEvery := fs.Int("drop-every", 0, "close the connection deliberately every `seconds` and reconnect after 2 seconds; 0 disables")
	duration := fs.Int("duration", 0, "stop generating after `seconds`, wait for the last acks and exit; 0 runs until Ctrl+C")
	class := fs.String("class", "esp", "device class reported in hello: esp, pi or pc")
	logLevel := fs.String("log-level", "info", "debug, info, warn or error")
	showVersion := fs.Bool("version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "simtarget: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "simtarget %s (%s)\n", version.Version, version.GoVersion())
		return 0
	}
	if *id == "" || *token == "" {
		fmt.Fprintln(stderr, "simtarget: --id and --token are required")
		return 2
	}
	if *rate <= 0 || *controllers < 1 || *dropEvery < 0 || *duration < 0 {
		fmt.Fprintln(stderr, "simtarget: --rate and --controllers must be positive, --drop-every and --duration not negative")
		return 2
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		fmt.Fprintf(stderr, "simtarget: %v\n", err)
		return 2
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	if *insecure {
		fmt.Fprintln(stderr, "simtarget: warning: --insecure skips TLS verification; use it only against a test server")
	}
	if *journalDir == "" {
		*journalDir = filepath.Join("data", "simtarget", *id)
	}
	journal, err := simtarget.OpenJournal(*journalDir)
	if err != nil {
		fmt.Fprintf(stderr, "simtarget: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	started := time.Now()
	stats, err := simtarget.Run(ctx, simtarget.Options{
		Server:      *server,
		DeviceID:    *id,
		Token:       *token,
		Class:       *class,
		Firmware:    "simtarget " + version.Version,
		Insecure:    *insecure,
		Rate:        *rate,
		Controllers: *controllers,
		DropEvery:   time.Duration(*dropEvery) * time.Second,
		Duration:    time.Duration(*duration) * time.Second,
		Logger:      logger,
	}, journal)

	fmt.Fprintf(stdout, "simtarget %s summary after %s\n", *id, time.Since(started).Round(time.Millisecond))
	firstSeq := "none"
	if stats.Generated > 0 {
		firstSeq = fmt.Sprint(stats.FirstSeq)
	}
	fmt.Fprintf(stdout, "  generated:   %d events this run, seq %s to %d\n", stats.Generated, firstSeq, stats.LastSeq)
	fmt.Fprintf(stdout, "  frames sent: %d, of which replayed: %d in %d replays\n", stats.FramesSent, stats.Replayed, stats.Replays)
	fmt.Fprintf(stdout, "  connections: %d, deliberate drops: %d\n", stats.Connections, stats.Drops)
	fmt.Fprintf(stdout, "  commands:    %d answered\n", stats.Commands)
	fmt.Fprintf(stdout, "  last ack:    %d, unacknowledged: %d\n", stats.LastAck, stats.Unacked)
	fmt.Fprintf(stdout, "  journal:     %s\n", *journalDir)

	switch {
	case errors.Is(err, simtarget.ErrUnauthorized):
		fmt.Fprintf(stderr, "simtarget: %v\n", err)
		return 3
	case err != nil:
		fmt.Fprintf(stderr, "simtarget: %v\n", err)
		return 1
	case stats.Unacked > 0:
		fmt.Fprintf(stderr, "simtarget: %d events are not acknowledged; they stay in the journal for the next run\n", stats.Unacked)
		return 1
	}
	return 0
}
