// Command theserver is the local venue server of the CYB3RGUN system.
//
// This first version loads the configuration, sets up logging and serves a
// health endpoint over plain HTTP until it is interrupted.
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// After the first signal, restore the default handling so that a
		// second Ctrl+C ends the process at once.
		<-ctx.Done()
		stop()
	}()

	if err := serve(ctx, cfg, logger); err != nil {
		logger.Error("theserver stopped with an error", "error", err)
		return 1
	}
	return 0
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
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", cfg.Server.ListenAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           newMux(),
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

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	return mux
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: version.Version})
}
