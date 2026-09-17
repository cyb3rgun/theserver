// Package scenariotest hands the fixtures of pkg/scenario to the tests
// of other packages: a valid VIDEO and a valid INTERACTIVE package, and one
// broken package per problem code, as a directory or as the zip a person
// uploads.
package scenariotest

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The valid fixtures, by name.
const (
	// Video is night-range, version 1, rated 12.
	Video = "valid/video"
	// Interactive is zombie-alley, version 1, rated 18.
	Interactive = "valid/interactive"
	// Layered is dark-alley, version 1, rated 18, with two character layers.
	Layered = "valid/layered"
)

// zipTime is the time of every zip entry, so the same files give the same
// bytes.
var zipTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// Broken names the fixture called name, which fails with exactly one
// problem; the name is the problem code where a code has one fixture.
// bad_package is a zip, every other one a directory.
func Broken(name string) string {
	if name == scenario.CodeBadPackage {
		return "invalid/" + name + ".zip"
	}
	return "invalid/" + name
}

// Dir returns the path of the fixture name.
func Dir(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata", filepath.FromSlash(name))
}

// Zip returns the fixture name as a zip with its files at the root.
func Zip(t testing.TB, name string) []byte {
	t.Helper()
	if strings.HasSuffix(name, ".zip") {
		data, err := os.ReadFile(Dir(name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	data, err := ZipFiles(Files(t, Dir(name)), "")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// WriteZip writes the fixture name as a zip file into dir and returns its
// path.
func WriteZip(t testing.TB, name, dir string) string {
	t.Helper()
	file := filepath.Join(dir, path.Base(strings.TrimSuffix(name, ".zip"))+".zip")
	if err := os.WriteFile(file, Zip(t, name), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// Files reads every file below dir, keyed by its path with forward slashes.
func Files(t testing.TB, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := fs.WalkDir(os.DirFS(dir), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		files[name] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// ZipFiles packs files, keyed by path, into a zip, each under prefix, in the
// order of their paths and with a fixed time, so the same files give the
// same bytes.
func ZipFiles(files map[string][]byte, prefix string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f, err := w.CreateHeader(&zip.FileHeader{Name: prefix + name, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
