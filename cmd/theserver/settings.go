package main

import (
	"fmt"
	"io"
	"os"

	"github.com/cyb3rgun/theserver/internal/settings"
)

// runSettings handles theserver settings doc, which writes the settings
// reference docs/settings.md from the registry (D-030).
func runSettings(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "doc" {
		fmt.Fprintf(stderr, "theserver: settings needs doc\n\n%s", usage)
		return 2
	}
	fs, _ := newFlagSet("settings doc", stderr)
	out := fs.String("out", "", "write the reference to `path` instead of the standard output")
	if code := parse(fs, args[1:], stderr); code >= 0 {
		return code
	}
	doc := settings.Markdown()
	if *out == "" {
		if _, err := stdout.Write(doc); err != nil {
			return 1
		}
		return 0
	}
	if err := os.WriteFile(*out, doc, 0o644); err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "theserver: wrote the settings reference to %s\n", *out)
	return 0
}
