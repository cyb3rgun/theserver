package simtarget

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cyb3rgun/theserver/internal/protocol"
	"github.com/cyb3rgun/theserver/internal/scenario"
)

// The simulated target keeps what it installs in its content directory,
// <id>/<version>/package.zip, and reports it in every health event
// (protocol section 8.10). An announcement is taken at once; the package is
// fetched over HTTPS with the device token, a cut download is resumed with
// Range, and the package counts as installed only when its size, its
// manifest hash and its validation agree with the announcement.

// downloadAttempts bounds how often a cut download is resumed.
const downloadAttempts = 3

// maxReason bounds the reason of a failed install in a content event, which
// has to fit a frame.
const maxReason = 300

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// permanent marks a download failure that a retry does not fix.
type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// ParseHoldings reads a list such as night-range@1,zombie-alley@2.
func ParseHoldings(list string) ([]protocol.Holding, error) {
	var holdings []protocol.Holding
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, ver, ok := strings.Cut(item, "@")
		version, err := strconv.ParseUint(ver, 10, 32)
		if !ok || err != nil || version < 1 || !scenario.ValidID(id) {
			return nil, fmt.Errorf("holding %q is not id@version", item)
		}
		holdings = append(holdings, protocol.Holding{ID: id, Ver: version})
	}
	return holdings, nil
}

// FormatHoldings writes holdings as ParseHoldings reads them.
func FormatHoldings(holdings []protocol.Holding) string {
	names := make([]string, len(holdings))
	for i, h := range holdings {
		names[i] = h.ID + "@" + strconv.FormatUint(h.Ver, 10)
	}
	return strings.Join(names, ",")
}

// installed lists the packages in the content directory.
func installed(dir string) []protocol.Holding {
	var holdings []protocol.Holding
	ids, _ := os.ReadDir(dir)
	for _, id := range ids {
		if !id.IsDir() || !scenario.ValidID(id.Name()) {
			continue
		}
		versions, _ := os.ReadDir(filepath.Join(dir, id.Name()))
		for _, v := range versions {
			version, err := strconv.ParseUint(v.Name(), 10, 32)
			if err != nil || version < 1 {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, id.Name(), v.Name(), "package.zip")); err == nil {
				holdings = append(holdings, protocol.Holding{ID: id.Name(), Ver: version})
			}
		}
	}
	return holdings
}

// holdings lists what the target holds, by id and version; never nil, so a
// health event always reports it.
func (d *device) holdings() []protocol.Holding {
	d.heldMu.Lock()
	defer d.heldMu.Unlock()
	list := make([]protocol.Holding, 0, len(d.held))
	for h := range d.held {
		list = append(list, h)
	}
	slices.SortFunc(list, func(a, b protocol.Holding) int {
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return int(a.Ver) - int(b.Ver)
	})
	return list
}

// announced answers content_available: the target takes a usable
// announcement and installs in the background, unless it holds the version
// already or installs it right now.
func (d *device) announced(ctx context.Context, cmd protocol.Command) protocol.Result {
	refuse := func(reason string) protocol.Result {
		return protocol.Result{ID: cmd.ID, OK: false, E: reason}
	}
	var content protocol.ContentAvailable
	if err := protocol.DecodeArgs(cmd.A, &content); err != nil {
		return refuse(err.Error())
	}
	if !scenario.ValidID(content.ID) || content.Ver < 1 || content.Ver > 1<<31-1 || !shaPattern.MatchString(content.Sha) {
		return refuse("the announcement names no usable scenario version")
	}
	if d.opts.ContentDir == "" {
		return refuse("simtarget runs without a content directory")
	}
	holding := protocol.Holding{ID: content.ID, Ver: content.Ver}
	d.heldMu.Lock()
	defer d.heldMu.Unlock()
	switch {
	case d.held[holding]:
		d.log.Info("content announced, already held", "scenario", content.ID, "version", content.Ver)
		return protocol.Result{ID: cmd.ID, OK: true, R: map[string]any{"held": true}}
	case d.installing[holding]:
		return protocol.Result{ID: cmd.ID, OK: true, R: map[string]any{"installing": true}}
	}
	d.installing[holding] = true
	d.installs.Add(1)
	go func() {
		defer d.installs.Done()
		d.install(ctx, content)
	}()
	return protocol.Result{ID: cmd.ID, OK: true}
}

// install fetches an announced version and reports each step as a content
// event.
func (d *device) install(ctx context.Context, content protocol.ContentAvailable) {
	holding := protocol.Holding{ID: content.ID, Ver: content.Ver}
	defer func() {
		d.heldMu.Lock()
		delete(d.installing, holding)
		d.heldMu.Unlock()
	}()
	log := d.log.With("scenario", content.ID, "version", content.Ver)
	log.Info("content announced, fetching", "size", content.Size, "sha256", content.Sha)
	d.report(content, protocol.ContentInstalling, "")

	started := time.Now()
	if err := d.fetch(ctx, content); err != nil {
		log.Warn("install failed", "error", err)
		d.mu.Lock()
		d.stats.InstallsFailed++
		d.mu.Unlock()
		d.report(content, protocol.ContentFailed, err.Error())
		return
	}
	d.heldMu.Lock()
	d.held[holding] = true
	d.heldMu.Unlock()
	d.mu.Lock()
	d.stats.Installs++
	d.mu.Unlock()
	log.Info("content installed", "size", content.Size, "took", time.Since(started).Round(time.Millisecond))
	d.report(content, protocol.ContentInstalled, "")
}

// report journals a content event.
func (d *device) report(content protocol.ContentAvailable, state, reason string) {
	if len(reason) > maxReason {
		reason = reason[:maxReason]
	}
	draft := mustDraft(protocol.KindContent, protocol.ContentData{ID: content.ID, Ver: content.Ver, St: state, E: reason})
	if err := d.appendDraft(draft); err != nil {
		d.log.Warn("could not journal the content report", "error", err)
	}
}

// fetch downloads the package, checks it against the announcement and keeps
// it in the content directory.
func (d *device) fetch(ctx context.Context, content protocol.ContentAvailable) error {
	if err := os.MkdirAll(d.opts.ContentDir, 0o755); err != nil {
		return err
	}
	version := strconv.FormatUint(content.Ver, 10)
	part := filepath.Join(d.opts.ContentDir, "."+content.ID+"-"+version+".part")
	defer os.Remove(part)
	address := d.baseURL + "/api/v1/scenarios/" + url.PathEscape(content.ID) + "/" + version + "/package.zip"

	for attempt := 1; ; attempt++ {
		err := d.download(ctx, address, part, content)
		if err == nil {
			break
		}
		var fatal permanent
		if attempt == downloadAttempts || errors.As(err, &fatal) || ctx.Err() != nil {
			return err
		}
		d.log.Info("download cut off, resuming", "scenario", content.ID, "version", content.Ver, "attempt", attempt, "error", err)
		time.Sleep(100 * time.Millisecond)
	}

	info, err := os.Stat(part)
	if err != nil {
		return err
	}
	if uint64(info.Size()) != content.Size {
		return fmt.Errorf("the package has %d bytes, the announcement says %d", info.Size(), content.Size)
	}
	pkg, err := scenario.LoadZip(part)
	if err != nil {
		return err
	}
	if hash := scenario.Hash(pkg.Manifest); hash != content.Sha {
		return fmt.Errorf("the manifest hash %s does not match the announced %s", hash, content.Sha)
	}
	if info := pkg.Manifest.Scenario; info.ID != content.ID || uint64(info.Version) != content.Ver {
		return fmt.Errorf("the package is %s version %d", info.ID, info.Version)
	}
	if problems := pkg.Validate(); len(problems) > 0 {
		return fmt.Errorf("the package is not valid: %s", problems[0])
	}
	dir := filepath.Join(d.opts.ContentDir, content.ID, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.Rename(part, filepath.Join(dir, "package.zip"))
}

// download fetches the package into part, from where an earlier attempt
// stopped.
func (d *device) download(ctx context.Context, address, part string, content protocol.ContentAvailable) error {
	// Not O_APPEND: Windows refuses to truncate such a file, which a server
	// that ignores Range needs.
	f, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return permanent{err}
	}
	req.Header.Set("Authorization", "Bearer "+d.opts.Token)
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		if offset > 0 {
			// The server ignored Range and sends the whole package again.
			if err := f.Truncate(0); err != nil {
				return err
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			offset = 0
		}
	case resp.StatusCode == http.StatusPartialContent && offset > 0:
		if !strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes "+strconv.FormatInt(offset, 10)+"-") {
			return permanent{fmt.Errorf("the server resumed at %q, not at byte %d", resp.Header.Get("Content-Range"), offset)}
		}
	default:
		return permanent{fmt.Errorf("the server answered %s", resp.Status)}
	}
	if sha := resp.Header.Get("X-Manifest-SHA256"); sha != "" && sha != content.Sha {
		return permanent{fmt.Errorf("the server offers the manifest hash %s, the announcement says %s", sha, content.Sha)}
	}
	written, err := io.Copy(f, io.LimitReader(resp.Body, int64(content.Size)+1))
	switch {
	case err != nil:
		return err
	case uint64(offset+written) > content.Size && resp.StatusCode == http.StatusPartialContent,
		uint64(written) > content.Size:
		return permanent{fmt.Errorf("the package is larger than the announced %d bytes", content.Size)}
	}
	return nil
}

// httpBase turns the address of the link into the address of the API.
func httpBase(server string) (string, error) {
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	default:
		return "", fmt.Errorf("server %s is not a wss address", server)
	}
	u.Path, u.RawQuery = "", ""
	return u.String(), nil
}

func httpClient(insecure bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecure,
	}
	return &http.Client{Transport: transport}
}
