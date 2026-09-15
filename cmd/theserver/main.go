// Command theserver is the local venue server of the CYB3RGUN system.
//
// This version loads the configuration, sets up logging, opens the SQLite
// database in the data directory and serves a health endpoint over plain HTTP
// until it is interrupted.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cyb3rgun/theserver/internal/config"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/internal/version"
)

// shutdownGrace is how long open requests may take to finish after an
// interrupt before the server closes them.
const shutdownGrace = 5 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("theserver", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "read the TOML configuration file at `path`")
	fs.String("data-dir", "", "directory for runtime data, overrides server.data_dir")
	fs.String("listen", "", "address and port to listen on, overrides server.listen_addr")
	fs.String("log-level", "", "debug, info, warn or error, overrides log.level")
	showVersion := fs.Bool("version", false, "print version information and exit")
	writeDefault := fs.String("write-default-config", "", "write a configuration file with every default to `path` and exit")
	dbInfo := fs.Bool("db-info", false, "print the state of the database and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "theserver: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "theserver %s\ncommit:     %s\nbuild date: %s\ngo:         %s\n",
			version.Version, version.Commit, version.BuildDate, version.GoVersion())
		return 0
	}

	if *writeDefault != "" {
		if err := config.WriteDefault(*writeDefault); err != nil {
			fmt.Fprintf(stderr, "theserver: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "theserver: wrote default configuration to %s\n", *writeDefault)
		return 0
	}

	setFlags := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = f.Value.String()
	})
	cfg, err := config.Load(*configPath, os.LookupEnv, setFlags)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// After the first signal, restore the default handling so that a
		// second Ctrl+C ends the process at once.
		<-ctx.Done()
		stop()
	}()

	if *dbInfo {
		if err := printDBInfo(ctx, cfg, stdout); err != nil {
			fmt.Fprintf(stderr, "theserver: %v\n", err)
			return 1
		}
		return 0
	}

	logger := newLogger(cfg.Log, stderr)
	slog.SetDefault(logger)
	logger.Info("theserver starting",
		"version", version.Version,
		"commit", version.Commit,
		"build_date", version.BuildDate,
		"go", version.GoVersion(),
		"config_file", *configPath,
		"config", cfg,
	)

	db, err := openStore(ctx, cfg)
	if err != nil {
		logger.Error("theserver could not open the database", "error", err)
		return 1
	}
	defer db.Close()

	schemaVersion, err := db.SchemaVersion(ctx)
	if err != nil {
		logger.Error("theserver could not read the schema version", "error", err)
		return 1
	}
	logger.Info("database ready", "path", db.Path(), "schema_version", schemaVersion)

	if err := serve(ctx, cfg, db, logger); err != nil {
		logger.Error("theserver stopped with an error", "error", err)
		return 1
	}
	return 0
}

func openStore(ctx context.Context, cfg config.Config) (*store.Store, error) {
	return store.Open(ctx, cfg.Server.DataDir,
		store.WithBusyTimeout(time.Duration(cfg.Store.BusyTimeoutMs)*time.Millisecond))
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

func newLogger(c config.Log, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.SlogLevel()}
	if c.Format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// serve runs the HTTP server until ctx is done, then shuts it down, giving
// open requests shutdownGrace to finish.
func serve(ctx context.Context, cfg config.Config, db *store.Store, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", cfg.Server.ListenAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           newMux(db, logger),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	served := make(chan error, 1)
	go func() {
		served <- srv.Serve(ln)
	}()
	logger.Info("listening", "addr", ln.Addr().String())

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down", "grace", shutdownGrace)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		srv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("theserver stopped cleanly")
	return nil
}

func newMux(db *store.Store, logger *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(db, logger))
	return mux
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	DB      string `json:"db"`
}

// healthzHandler answers 200 while the database answers, and 503 when it does
// not, so that a monitor sees the difference.
func healthzHandler(db *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := healthResponse{Status: "ok", Version: version.Version, DB: "ok"}
		status := http.StatusOK
		if err := db.Ping(r.Context()); err != nil {
			logger.Error("health check could not reach the database", "error", err)
			body = healthResponse{Status: "error", Version: version.Version, DB: "error"}
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}
}
