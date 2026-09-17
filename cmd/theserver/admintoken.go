package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// runAdmin manages the admin tokens of API v1 and the admin pages (D-025).
func runAdmin(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "token" {
		fmt.Fprintf(stderr, "theserver: admin needs token add, list or revoke\n\n%s", usage)
		return 2
	}
	switch args[1] {
	case "add":
		return adminTokenAdd(args[2:], stdout, stderr)
	case "list":
		return adminTokenList(args[2:], stdout, stderr)
	case "revoke":
		return adminTokenRevoke(args[2:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "theserver: unknown admin token command %q\n\n%s", args[1], usage)
		return 2
	}
}

func adminTokenAdd(args []string, stdout, stderr io.Writer) int {
	fs, configPath := newFlagSet("admin token add", stderr)
	name := fs.String("name", "", "who or what the token is for, for example founder")
	if code := parse(fs, args, stderr); code >= 0 {
		return code
	}
	if strings.TrimSpace(*name) == "" {
		fmt.Fprintln(stderr, "theserver: admin token add needs --name")
		return 2
	}
	ctx := context.Background()
	db, code := openFromFlags(ctx, fs, *configPath, stderr)
	if db == nil {
		return code
	}
	defer db.Close()

	row, token, err := db.AddAdminToken(ctx, *name)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "admin token %s added for %s\n", row.ID, row.Name)
	fmt.Fprintf(stdout, "token: %s\n", token)
	fmt.Fprintln(stdout, "warning: this token is shown once and cannot be shown again; keep it like a password")
	fmt.Fprintln(stdout, "use it as Authorization: Bearer <token> for /api/v1, or paste it at /admin/login")
	return 0
}

func adminTokenList(args []string, stdout, stderr io.Writer) int {
	fs, configPath := newFlagSet("admin token list", stderr)
	if code := parse(fs, args, stderr); code >= 0 {
		return code
	}
	ctx := context.Background()
	db, code := openFromFlags(ctx, fs, *configPath, stderr)
	if db == nil {
		return code
	}
	defer db.Close()

	tokens, err := db.ListAdminTokens(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME\tCREATED\tLAST USED\tREVOKED")
	for _, t := range tokens {
		revoked := "no"
		if t.Revoked() {
			revoked = when(t.RevokedAt)
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, when(t.CreatedAt), when(t.LastUsed), revoked)
	}
	if err := table.Flush(); err != nil {
		return 1
	}
	if len(tokens) == 0 {
		fmt.Fprintln(stdout, "no admin tokens yet; add one with: theserver admin token add --name <name>")
	}
	return 0
}

func adminTokenRevoke(args []string, stdout, stderr io.Writer) int {
	fs, configPath := newFlagSet("admin token revoke", stderr)
	idFlag := fs.String("id", "", "admin token id, as admin token list shows it")
	id, code := parseWithID(fs, args, idFlag, stderr)
	if code >= 0 {
		return code
	}
	if id == "" {
		fmt.Fprintln(stderr, "theserver: admin token revoke needs the token id")
		return 2
	}
	ctx := context.Background()
	db, code := openFromFlags(ctx, fs, *configPath, stderr)
	if db == nil {
		return code
	}
	defer db.Close()

	if err := db.RevokeAdminToken(ctx, id); err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "admin token %s revoked; API requests with it get 403 and its admin logins end\n", id)
	return 0
}

// parseWithID parses flags that may come before or after one positional id,
// which can also be given as --id. It returns the id and -1, or an exit code
// of 0 or more when the command has to stop.
func parseWithID(fs *flag.FlagSet, args []string, idFlag *string, stderr io.Writer) (string, int) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return "", 0
			}
			return "", 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	switch {
	case len(positional) > 1:
		fmt.Fprintf(stderr, "theserver: unexpected arguments: %s\n", strings.Join(positional[1:], " "))
		return "", 2
	case len(positional) == 1 && *idFlag != "" && *idFlag != positional[0]:
		fmt.Fprintf(stderr, "theserver: two different ids: %s and --id %s\n", positional[0], *idFlag)
		return "", 2
	case len(positional) == 1:
		return positional[0], -1
	default:
		return *idFlag, -1
	}
}

func when(ms int64) string {
	if ms == 0 {
		return "never"
	}
	return time.UnixMilli(ms).Local().Format(time.DateTime)
}
