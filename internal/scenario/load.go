package scenario

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load reads the package in the directory dir: the manifest at its root and
// the SHA-256 of every other file. A package that cannot be read is a
// *LoadError; what the package holds is checked by Validate.
func Load(dir string) (*Package, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, packageError(dir, "the package directory cannot be read: "+err.Error(), err)
	}
	if !info.IsDir() {
		return nil, packageError(dir, dir+" is not a directory", nil)
	}
	fsys := os.DirFS(dir)
	files := map[string]string{}
	var manifest []byte
	found := false
	err = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return packageError(name, "cannot be read: "+err.Error(), err)
		}
		switch {
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return packageError(name, name+" is not a regular file", nil)
		}
		open := func() (io.ReadCloser, error) { return fsys.Open(name) }
		switch name {
		case ManifestName:
			found = true
			manifest, err = readAll(name, open)
			return err
		case SignatureName:
			return nil
		}
		sum, err := hashOf(name, open)
		files[name] = sum
		return err
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, noManifest()
	}
	m, err := ParseManifest(manifest)
	if err != nil {
		return nil, err
	}
	return &Package{Manifest: m, Files: files}, nil
}

// LoadZip reads the package in the zip file at path. The files are at the
// root of the zip, or all inside one top directory, as a zipped package
// directory has them.
func LoadZip(path string) (*Package, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, packageError(path, "the package cannot be opened: "+err.Error(), err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, packageError(path, "the package cannot be read: "+err.Error(), err)
	}
	return ReadZip(f, info.Size())
}

// ReadZip is LoadZip for a zip of size bytes read through r.
func ReadZip(r io.ReaderAt, size int64) (*Package, error) {
	zr, prefix, err := openZip(r, size)
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	var manifest []byte
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		name := strings.TrimPrefix(f.Name, prefix)
		switch name {
		case ManifestName:
			if manifest, err = readAll(name, f.Open); err != nil {
				return nil, err
			}
		case SignatureName:
		default:
			if files[name], err = hashOf(name, f.Open); err != nil {
				return nil, err
			}
		}
	}
	m, err := ParseManifest(manifest)
	if err != nil {
		return nil, err
	}
	return &Package{Manifest: m, Files: files}, nil
}

// OpenZipFile opens the file name, as Package.Files lists it, in the zip
// package at path, and returns it with its size. Closing it closes the zip.
func OpenZipFile(path, name string) (io.ReadCloser, int64, error) {
	zc, err := zip.OpenReader(path)
	if err != nil {
		return nil, 0, err
	}
	_, prefix, err := checkZip(&zc.Reader)
	if err != nil {
		zc.Close()
		return nil, 0, err
	}
	for _, f := range zc.File {
		if f.Name != prefix+name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			zc.Close()
			return nil, 0, err
		}
		return &zipFile{ReadCloser: rc, zip: zc}, int64(f.UncompressedSize64), nil
	}
	zc.Close()
	return nil, 0, fmt.Errorf("scenario package %s has no file %s: %w", path, name, fs.ErrNotExist)
}

type zipFile struct {
	io.ReadCloser
	zip *zip.ReadCloser
}

func (f *zipFile) Close() error {
	return errors.Join(f.ReadCloser.Close(), f.zip.Close())
}

// ParseManifest reads manifest.toml. Text that is not TOML, or a value of
// the wrong type, is a *LoadError with the code bad_manifest; keys the model
// does not know are left to Validate.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&m)
	if err != nil {
		return Manifest{}, &LoadError{
			Problem: Problem{Field: ManifestName, Code: CodeBadManifest, Detail: err.Error()},
			Err:     err,
		}
	}
	for _, key := range md.Undecoded() {
		m.unknown = append(m.unknown, key.String())
	}
	slices.Sort(m.unknown)
	return m, nil
}

// openZip reads the directory of a zip and finds where its package starts.
func openZip(r io.ReaderAt, size int64) (*zip.Reader, string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, "", packageError("", "the upload is not a readable zip: "+err.Error(), err)
	}
	return checkZip(zr)
}

// checkZip refuses names that could leave the package, repeat or are not
// regular files, and returns the prefix of the package: empty when the
// manifest is at the root, else the one top directory that holds it.
func checkZip(zr *zip.Reader) (*zip.Reader, string, error) {
	seen := map[string]bool{}
	tops := map[string]bool{}
	for _, f := range zr.File {
		name := f.Name
		clean := strings.TrimSuffix(name, "/")
		switch {
		case !fs.ValidPath(clean) || clean == "." || strings.ContainsAny(name, "\\:"):
			return nil, "", packageError(name, name+" is not a relative path inside the package", nil)
		case seen[name]:
			return nil, "", packageError(name, name+" is in the zip twice", nil)
		case !f.Mode().IsRegular() && !f.Mode().IsDir():
			return nil, "", packageError(name, name+" is not a regular file", nil)
		}
		seen[name] = true
		top, _, _ := strings.Cut(clean, "/")
		tops[top] = true
	}
	if seen[ManifestName] {
		return zr, "", nil
	}
	if len(tops) == 1 {
		for top := range tops {
			if seen[top+"/"+ManifestName] {
				return zr, top + "/", nil
			}
		}
	}
	return nil, "", noManifest()
}

func packageError(field, detail string, err error) *LoadError {
	return &LoadError{Problem: Problem{Field: field, Code: CodeBadPackage, Detail: detail}, Err: err}
}

func noManifest() *LoadError {
	return &LoadError{Problem: Problem{
		Field:  ManifestName,
		Code:   CodeNoManifest,
		Detail: "the package has no manifest.toml at its root or inside its one top directory",
	}}
}

func readAll(name string, open func() (io.ReadCloser, error)) ([]byte, error) {
	rc, err := open()
	if err != nil {
		return nil, packageError(name, name+" cannot be read: "+err.Error(), err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, packageError(name, name+" cannot be read: "+err.Error(), err)
	}
	return data, nil
}

func hashOf(name string, open func() (io.ReadCloser, error)) (string, error) {
	rc, err := open()
	if err != nil {
		return "", packageError(name, name+" cannot be read: "+err.Error(), err)
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", packageError(name, name+" cannot be read: "+err.Error(), err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
