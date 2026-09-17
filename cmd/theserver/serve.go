package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cyb3rgun/theserver/internal/admin"
	"github.com/cyb3rgun/theserver/internal/config"
	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/internal/tlsboot"
	"github.com/cyb3rgun/theserver/internal/version"
)

// shutdownGrace is how long open requests and device connections may take to
// finish after an interrupt.
const shutdownGrace = 5 * time.Second

func runServe(args []string, stdout, stderr io.Writer) int {
	fs, configPath := newFlagSet("serve", stderr)
	fs.String("listen", "", "address and port to listen on, overrides server.listen_addr")
	fs.String("log-level", "", "debug, info, warn or error, overrides log.level")
	showVersion := fs.Bool("version", false, "print version information and exit")
	writeDefault := fs.String("write-default-config", "", "write a configuration file with every default to `path` and exit")
	dbInfo := fs.Bool("db-info", false, "deprecated: use theserver db info")
	if code := parse(fs, args, stderr); code >= 0 {
		return code
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

	cfg, sources, err := loadConfigWithSources(fs, *configPath)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}

	if *dbInfo {
		fmt.Fprintln(stderr, "theserver: --db-info is deprecated and goes away after S01; use: theserver db info")
		return dbInfoCommand(cfg, stdout, stderr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// After the first signal, restore the default handling so that a
		// second Ctrl+C ends the process at once.
		<-ctx.Done()
		stop()
	}()

	logger := newLogger(cfg.Log, stderr)
	slog.SetDefault(logger)
	if err := serve(ctx, cfg, sources, *configPath, logger); err != nil {
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

// serve runs HTTPS with the health endpoint and the device link until ctx is
// done, then shuts down within shutdownGrace.
func serve(ctx context.Context, cfg config.Config, sources config.Sources, configPath string, logger *slog.Logger) error {
	logger.Info("theserver starting",
		"version", version.Version,
		"commit", version.Commit,
		"build_date", version.BuildDate,
		"go", version.GoVersion(),
		"config_file", configPath,
		"config", cfg,
	)

	db, err := openStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer db.Close()
	schemaVersion, err := db.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("read the schema version: %w", err)
	}
	logger.Info("database ready", "path", db.Path(), "schema_version", schemaVersion)

	certFile, keyFile, err := certificate(cfg, logger)
	if err != nil {
		return err
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load the certificate: %w", err)
	}

	deviceLink := link.New(db, linkConfig(cfg.Link), logger)
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	go deviceLink.Watch(watchCtx)

	ln, err := net.Listen("tcp", cfg.Server.ListenAddr)
	if err != nil {
		return err
	}
	router := httpapi.New(httpapi.Options{
		Store:    db,
		Link:     deviceLink,
		Settings: func() []config.Setting { return config.Describe(cfg, sources) },
		Logger:   logger,
	})
	key, err := admin.LoadOrCreateKey(filepath.Join(cfg.Server.DataDir, admin.KeyFileName))
	if err != nil {
		return fmt.Errorf("admin cookie key: %w", err)
	}
	pages, err := admin.New(admin.Options{API: router.API(), Store: db, Key: key, Logger: logger})
	if err != nil {
		return err
	}
	router.Mount("/admin", pages)
	router.Mount("/admin/", pages)

	srv := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{pair},
		},
	}

	served := make(chan error, 1)
	go func() {
		served <- srv.ServeTLS(ln, "", "")
	}()
	logger.Info("listening", "addr", ln.Addr().String(), "scheme", "https", "device_link", link.Path, "api", httpapi.Prefix, "admin", "/admin")

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
	// Device connections are hijacked, so Shutdown does not wait for them;
	// the link closes them and lets each flush what it received.
	if err := deviceLink.Close(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("theserver stopped cleanly")
	return nil
}

// certificate returns the certificate to serve and logs its fingerprint. With
// no files configured it is the one in <data_dir>/tls, created on first start
// (D-018).
func certificate(cfg config.Config, logger *slog.Logger) (certFile, keyFile string, err error) {
	if cfg.TLS.CertFile != "" {
		fingerprint, err := tlsboot.FingerprintFile(cfg.TLS.CertFile)
		if err != nil {
			return "", "", fmt.Errorf("read the certificate: %w", err)
		}
		logger.Info("tls certificate", "source", "configured",
			"cert", cfg.TLS.CertFile, "key", cfg.TLS.KeyFile, "sha256", fingerprint)
		return cfg.TLS.CertFile, cfg.TLS.KeyFile, nil
	}

	dir := filepath.Join(cfg.Server.DataDir, "tls")
	certFile, keyFile, fingerprint, created, err := tlsboot.EnsureCertificate(dir, tlsboot.DefaultHosts())
	if err != nil {
		return "", "", fmt.Errorf("self signed certificate: %w", err)
	}
	source := "self signed, existing"
	if created {
		source = "self signed, created now"
	}
	logger.Info("tls certificate", "source", source, "cert", certFile, "key", keyFile, "sha256", fingerprint)
	return certFile, keyFile, nil
}

func linkConfig(c config.Link) link.Config {
	cfg := link.DefaultConfig()
	cfg.AckInterval = time.Duration(c.AckIntervalMs) * time.Millisecond
	cfg.AckBatch = c.AckBatch
	cfg.PingInterval = time.Duration(c.PingIntervalS) * time.Second
	cfg.PongTimeout = time.Duration(c.PongTimeoutS) * time.Second
	cfg.HelloTimeout = time.Duration(c.HelloTimeoutS) * time.Second
	return cfg
}

// Make sure the store satisfies what the router needs.
var _ httpapi.Pinger = (*store.Store)(nil)
