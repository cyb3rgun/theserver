// Command theserver is the local venue server of the CYB3RGUN system.
//
// It has a few subcommands (D-022):
//
//	theserver [serve] [flags]              run the server; the default
//	theserver device add|list|reset|revoke manage devices and their tokens
//	theserver admin token add|list|revoke  manage admin tokens for the API
//	theserver settings doc                 write the settings reference
//	theserver db info                      show the state of the database
//
// Every subcommand reads the same layered configuration, so --config and
// --data-dir mean the same everywhere.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cyb3rgun/theserver/internal/config"
	"github.com/cyb3rgun/theserver/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage:
  theserver [serve] [--config path] [--data-dir dir] [--listen addr] [--log-level level]
  theserver serve --version
  theserver serve --write-default-config path
  theserver device add --id id --kind target|controller|bridge [--class esp|pi|pc] [--name n] [--room r] [--zone z] [--min-age 0|6|12|16|18]
  theserver device list
  theserver device reset id
  theserver device revoke --id id
  theserver admin token add --name name
  theserver admin token list
  theserver admin token revoke id
  theserver settings doc [--out path]
  theserver db info

Every subcommand also takes --config and --data-dir.
`

// run dispatches to a subcommand. Without one, or when the first argument is a
// flag, it serves, so the usage of earlier passes keeps working.
func run(args []string, stdout, stderr io.Writer) int {
	command, rest := "serve", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, rest = args[0], args[1:]
	}

	switch command {
	case "serve":
		return runServe(rest, stdout, stderr)
	case "device":
		return runDevice(rest, stdout, stderr)
	case "admin":
		return runAdmin(rest, stdout, stderr)
	case "settings":
		return runSettings(rest, stdout, stderr)
	case "db":
		return runDB(rest, stdout, stderr)
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "theserver: unknown command %q\n\n%s", command, usage)
		return 2
	}
}

// newFlagSet returns a flag set with the flags every subcommand shares and
// the pointer to --config.
func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "read the TOML configuration file at `path`")
	fs.String("data-dir", "", "directory for runtime data, overrides server.data_dir")
	return fs, configPath
}

// parse parses args and refuses leftovers. It returns the exit code to use when
// the command should stop, or -1 to go on.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer) int {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "theserver: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	return -1
}

// loadConfig reads the configuration with the flags that were set.
func loadConfig(fs *flag.FlagSet, configPath string) (config.Config, error) {
	cfg, _, err := loadConfigWithSources(fs, configPath)
	return cfg, err
}

// loadConfigWithSources also reports where every setting came from.
func loadConfigWithSources(fs *flag.FlagSet, configPath string) (config.Config, config.Sources, error) {
	set := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = f.Value.String()
	})
	return config.LoadWithSources(configPath, os.LookupEnv, set)
}

func openStore(ctx context.Context, cfg config.Config) (*store.Store, error) {
	return store.Open(ctx, cfg.Server.DataDir,
		store.WithBusyTimeout(time.Duration(cfg.Store.BusyTimeoutMs)*time.Millisecond))
}

// openFromFlags loads the configuration and opens the store. On failure it
// reports the error and returns a nil store with the exit code.
func openFromFlags(ctx context.Context, fs *flag.FlagSet, configPath string, stderr io.Writer) (*store.Store, int) {
	cfg, err := loadConfig(fs, configPath)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return nil, 1
	}
	db, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return nil, 1
	}
	return db, 0
}
