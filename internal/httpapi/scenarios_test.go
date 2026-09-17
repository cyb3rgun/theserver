package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario"
	"github.com/cyb3rgun/theserver/pkg/scenario/scenariotest"
)

// upload sends data as the package field of a multipart body.
func (h *apiHarness) upload(data []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("note", "ignored")
	part, err := form.CreateFormFile(UploadField, "package.zip")
	if err != nil {
		h.t.Fatal(err)
	}
	part.Write(data)
	form.Close()
	req := httptest.NewRequest(http.MethodPost, Prefix+"/scenarios", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.token)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

// withManifest is the fixture name zipped with old replaced by new in its
// manifest.
func withManifest(t *testing.T, name, old, new string) []byte {
	t.Helper()
	files := scenariotest.Files(t, scenariotest.Dir(name))
	manifest := string(files[scenario.ManifestName])
	if !strings.Contains(manifest, old) {
		t.Fatalf("the manifest of %s lacks %q", name, old)
	}
	files[scenario.ManifestName] = []byte(strings.Replace(manifest, old, new, 1))
	data, err := scenariotest.ZipFiles(files, "")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func problemCodes(problems []scenario.Problem) string {
	var codes []string
	for _, p := range problems {
		codes = append(codes, p.Code)
	}
	return strings.Join(codes, ",")
}

// Upload, replace, validate, publish and delete, as the catalogue sees it
// (D-036, D-037).
func TestScenarioUploadPublishAndCatalogue(t *testing.T) {
	h := newAPI(t)
	video := scenariotest.Zip(t, scenariotest.Video)

	created := h.upload(video)
	up := decode[Upload](t, created, http.StatusCreated)
	if up.Replaced || up.Scenario.ID != "night-range" || up.Scenario.Version != 1 || up.Scenario.Status != "draft" ||
		len(up.Problems) != 0 || up.Scenario.Size != int64(len(video)) || up.Scenario.UploadedBy != "tests" ||
		up.Scenario.Title["de"] != "Nachtschießstand" || created.Header().Get("Location") != Prefix+"/scenarios/night-range" {
		t.Fatalf("the upload answered %+v", up)
	}
	list := decode[ScenarioList](t, h.call("GET", Prefix+"/scenarios", ""), 200)
	if len(list.Scenarios) != 1 || list.Scenarios[0].Latest != nil || len(list.Scenarios[0].Drafts) != 1 ||
		list.Scenarios[0].Current.Version != 1 || list.Scenarios[0].Current.Status != "draft" {
		t.Fatalf("the catalogue is %+v", list)
	}

	// A broken package of the same version replaces the draft and keeps its
	// problems; it cannot be published.
	broken := decode[Upload](t, h.upload(scenariotest.Zip(t, scenariotest.Broken(scenario.CodeHashMismatch))), http.StatusOK)
	if !broken.Replaced || problemCodes(broken.Problems) != "hash_mismatch" || problemCodes(broken.Scenario.Problems) != "hash_mismatch" {
		t.Fatalf("the broken upload answered %+v", broken)
	}
	expectError(t, h.call("POST", Prefix+"/scenarios/night-range/1/publish", ""), http.StatusConflict, codeHasProblems)
	decode[Upload](t, h.upload(video), http.StatusOK)
	published := decode[ScenarioVersion](t, h.call("POST", Prefix+"/scenarios/night-range/1/publish", ""), 200)
	if published.Status != "published" || published.PublishedAt == 0 {
		t.Errorf("the published version is %+v", published)
	}
	expectError(t, h.call("POST", Prefix+"/scenarios/night-range/1/publish", ""), http.StatusConflict, codePublished)
	expectError(t, h.call("DELETE", Prefix+"/scenarios/night-range/1", ""), http.StatusConflict, codePublished)

	// Version 1 again is refused and nothing changes; version 2 is a draft.
	again := expectError(t, h.upload(video), http.StatusUnprocessableEntity, codeInvalidPackage)
	if problemCodes(again.Error.Problems) != "version_taken" || !strings.Contains(again.Error.Problems[0].Detail, "needs version 2") {
		t.Errorf("uploading version 1 again gave %+v", again.Error)
	}
	second := decode[Upload](t, h.upload(withManifest(t, scenariotest.Video, "version     = 1\n", "version     = 2\n")), http.StatusCreated)
	if second.Scenario.Version != 2 {
		t.Fatalf("version 2 came to %+v", second)
	}
	list = decode[ScenarioList](t, h.call("GET", Prefix+"/scenarios", ""), 200)
	if s := list.Scenarios[0]; s.Latest == nil || s.Latest.Version != 1 || s.Current.Version != 1 || len(s.Drafts) != 1 || s.Drafts[0].Version != 2 {
		t.Errorf("the catalogue is %+v", list)
	}
	detail := decode[ScenarioDetail](t, h.call("GET", Prefix+"/scenarios/night-range", ""), 200)
	if detail.Latest != 1 || len(detail.Versions) != 2 || detail.Versions[0].Version != 2 || len(detail.Holdings) != 0 {
		t.Errorf("the detail is %+v", detail)
	}
	if rec := h.call("DELETE", Prefix+"/scenarios/night-range/2", ""); rec.Code != http.StatusNoContent {
		t.Errorf("deleting the draft answered %d: %s", rec.Code, rec.Body.String())
	}
	expectError(t, h.call("DELETE", Prefix+"/scenarios/night-range/2", ""), http.StatusNotFound, codeNotFound)
	expectError(t, h.call("GET", Prefix+"/scenarios/nowhere", ""), http.StatusNotFound, codeNotFound)
	expectError(t, h.call("POST", Prefix+"/scenarios/night-range/two/publish", ""), http.StatusNotFound, codeNotFound)

	// Packages that cannot be stored, and requests that are no uploads.
	for _, code := range []string{scenario.CodeBadPackage, scenario.CodeBadManifest, scenario.CodeBadID} {
		refused := expectError(t, h.upload(scenariotest.Zip(t, scenariotest.Broken(code))), http.StatusUnprocessableEntity, codeInvalidPackage)
		if problemCodes(refused.Error.Problems) != code {
			t.Errorf("%s gave %+v", code, refused.Error.Problems)
		}
	}
	expectError(t, h.call("POST", Prefix+"/scenarios", `{"id": "x"}`), http.StatusBadRequest, codeBadRequest)
	var empty bytes.Buffer
	form := multipart.NewWriter(&empty)
	form.WriteField("other", "x")
	form.Close()
	req := httptest.NewRequest(http.MethodPost, Prefix+"/scenarios", &empty)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.token)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	expectError(t, rec, http.StatusBadRequest, codeBadRequest)

	// The upload limit is a live setting.
	if _, err := h.settings.Change(map[string]any{"content.max_upload_mb": 1}); err != nil {
		t.Fatal(err)
	}
	files := scenariotest.Files(t, scenariotest.Dir(scenariotest.Video))
	noise := make([]byte, 3<<19)
	rand.Read(noise)
	files["media/noise.bin"] = noise
	big, err := scenariotest.ZipFiles(files, "")
	if err != nil {
		t.Fatal(err)
	}
	tooBig := expectError(t, h.upload(big), http.StatusRequestEntityTooLarge, codeTooLarge)
	if !strings.Contains(tooBig.Error.Message, "1 MB") {
		t.Errorf("the refusal says %q", tooBig.Error.Message)
	}

	logs := h.logs.String()
	for _, want := range []string{
		`msg="scenario uploaded" component=api scenario=night-range version=1 problems=0 size=` + strconv.Itoa(len(video)),
		`status=draft replaced=false admin_token=` + h.admin.ID + ` admin_name=tests`,
		`msg="scenario published" component=api scenario=night-range version=1 manifest_hash=` + published.ManifestHash,
		`msg="scenario upload refused" component=api scenario=night-range version=1 problems=1`,
		`msg="scenario draft deleted" component=api scenario=night-range version=2`,
		`msg="scenario upload refused" component=api reason="too large" limit_mb=1`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log lacks %s", want)
		}
	}
}

// Devices fetch published packages with their token, with Range and the
// manifest hash; admins fetch every version and the cover (D-038).
func TestPackageDownloadAndCover(t *testing.T) {
	h := newAPI(t)
	video := scenariotest.Zip(t, scenariotest.Video)
	decode[Upload](t, h.upload(video), http.StatusCreated)
	published := decode[ScenarioVersion](t, h.call("POST", Prefix+"/scenarios/night-range/1/publish", ""), 200)
	decode[Upload](t, h.upload(withManifest(t, scenariotest.Video, "version     = 1\n", "version     = 2\n")), http.StatusCreated)
	approved, err := store.NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	h.device("tgt-01", store.StatusApproved, store.HashToken(approved))
	h.device("tgt-02", store.StatusPending, store.HashToken("pending-token"))
	path := Prefix + "/scenarios/night-range/1/package.zip"

	whole := h.callAs("GET", path, "", "Bearer "+approved)
	if whole.Code != 200 || !bytes.Equal(whole.Body.Bytes(), video) || whole.Header().Get("X-Manifest-SHA256") != published.ManifestHash ||
		whole.Header().Get("Content-Type") != "application/zip" || whole.Header().Get("Accept-Ranges") != "bytes" ||
		!strings.Contains(whole.Header().Get("Content-Disposition"), `filename="night-range-1.zip"`) {
		t.Fatalf("the download answered %d with %v", whole.Code, whole.Header())
	}

	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer "+approved)
	req.Header.Set("Range", "bytes=100-")
	part := httptest.NewRecorder()
	h.srv.ServeHTTP(part, req)
	wantRange := "bytes 100-" + strconv.Itoa(len(video)-1) + "/" + strconv.Itoa(len(video))
	if part.Code != http.StatusPartialContent || part.Header().Get("Content-Range") != wantRange || !bytes.Equal(part.Body.Bytes(), video[100:]) ||
		part.Header().Get("X-Manifest-SHA256") != published.ManifestHash {
		t.Errorf("the range request answered %d, %q", part.Code, part.Header().Get("Content-Range"))
	}
	head := h.callAs("HEAD", path, "", "Bearer "+approved)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(video)) {
		t.Errorf("HEAD answered %d with %d bytes", head.Code, head.Body.Len())
	}

	draftPath := Prefix + "/scenarios/night-range/2/package.zip"
	expectError(t, h.callAs("GET", draftPath, "", "Bearer "+approved), http.StatusNotFound, codeNotFound)
	if admin := h.call("GET", draftPath, ""); admin.Code != 200 {
		t.Errorf("an admin could not fetch the draft: %d", admin.Code)
	}
	expectError(t, h.callAs("GET", path, "", "Bearer pending-token"), http.StatusForbidden, codeForbidden)
	expectError(t, h.callAs("GET", path, "", "Bearer nobody"), http.StatusUnauthorized, codeUnauthorized)
	expectError(t, h.callAs("GET", path, "", ""), http.StatusUnauthorized, codeUnauthorized)
	expectError(t, h.callAs("GET", Prefix+"/scenarios/night-range/9/package.zip", "", "Bearer "+approved), http.StatusNotFound, codeNotFound)

	cover := h.call("GET", Prefix+"/scenarios/night-range/1/cover.png", "")
	wantCover, _ := os.ReadFile(filepath.Join(scenariotest.Dir(scenariotest.Video), scenario.CoverName))
	if cover.Code != 200 || cover.Header().Get("Content-Type") != "image/png" || !bytes.Equal(cover.Body.Bytes(), wantCover) {
		t.Errorf("the cover answered %d, %s", cover.Code, cover.Header().Get("Content-Type"))
	}
	expectError(t, h.callAs("GET", Prefix+"/scenarios/night-range/1/cover.png", "", "Bearer "+approved), http.StatusUnauthorized, codeUnauthorized)

	logs := h.logs.String()
	for _, want := range []string{
		`msg="package download" component=api scenario=night-range version=1 size=` + strconv.Itoa(len(video)) + ` device=tgt-01 by=device`,
		`device=tgt-01 range="bytes=100-" by=device`,
		`scenario=night-range version=2 size=`,
		`admin_token=` + h.admin.ID + ` admin_name=tests by=admin`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log lacks %s", want)
		}
	}
}

// Assignment checks the state, the publication and the ages, and announces
// to the devices of the session (D-038, D-039).
func TestAssignScenarioAndAges(t *testing.T) {
	h := newAPI(t)
	ctx := context.Background()
	decode[Upload](t, h.upload(scenariotest.Zip(t, scenariotest.Video)), http.StatusCreated)
	decode[Upload](t, h.upload(scenariotest.Zip(t, scenariotest.Interactive)), http.StatusCreated)
	decode[Upload](t, h.upload(withManifest(t, scenariotest.Video, "version     = 1\n", "version     = 2\n")), http.StatusCreated)
	for _, id := range []string{"night-range", "zombie-alley"} {
		decode[ScenarioVersion](t, h.call("POST", Prefix+"/scenarios/"+id+"/1/publish", ""), 200)
	}
	for _, id := range []string{"tgt-01", "tgt-03", "tgt-04", "tgt-05"} {
		h.device(id, store.StatusApproved, nil)
	}
	young := decode[Device](t, h.call("POST", Prefix+"/devices/tgt-03/min_age", `{"min_age": 16}`), 200)
	if young.MinAge != 16 {
		t.Fatalf("tgt-03 is set for %d", young.MinAge)
	}
	decode[Device](t, h.call("POST", Prefix+"/devices/tgt-04/min_age", `{"min_age": 6}`), 200)
	for _, body := range []string{`{"min_age": 14}`, `{}`, `{"min_age": "old"}`, `{"age": 12}`} {
		expectError(t, h.call("POST", Prefix+"/devices/tgt-01/min_age", body), http.StatusBadRequest, codeBadRequest)
	}
	expectError(t, h.call("POST", Prefix+"/devices/tgt-09/min_age", `{"min_age": 12}`), http.StatusNotFound, codeNotFound)

	decode[Session](t, h.call("POST", Prefix+"/sessions", `{"id": "evening"}`), http.StatusCreated)
	for _, id := range []string{"tgt-01", "tgt-03"} {
		decode[Session](t, h.call("POST", Prefix+"/sessions/evening/devices", `{"device_id": "`+id+`"}`), 200)
	}

	rated := expectError(t, h.call("POST", Prefix+"/sessions/evening/scenario", `{"id": "zombie-alley", "version": 1}`), http.StatusConflict, codeAgeRating)
	if !strings.Contains(rated.Error.Message, "zombie-alley version 1 is rated 18, but device tgt-03 is set for 16") {
		t.Errorf("the age refusal says %q", rated.Error.Message)
	}
	expectError(t, h.call("POST", Prefix+"/sessions/evening/scenario", `{"id": "night-range", "version": 2}`), http.StatusConflict, codeNotPublished)
	expectError(t, h.call("POST", Prefix+"/sessions/evening/scenario", `{"id": "night-range", "version": 7}`), http.StatusNotFound, codeNotFound)
	expectError(t, h.call("POST", Prefix+"/sessions/night/scenario", `{"id": "night-range", "version": 1}`), http.StatusNotFound, codeNotFound)
	for _, body := range []string{`{"id": "night-range"}`, `{"version": 1}`, `{"id": "night-range", "version": 1, "extra": 1}`} {
		expectError(t, h.call("POST", Prefix+"/sessions/evening/scenario", body), http.StatusBadRequest, codeBadRequest)
	}

	h.link.answer = []link.Announcement{
		{DeviceID: "tgt-01", SessionID: "evening", ScenarioID: "night-range", Version: 1},
		{DeviceID: "tgt-03", SessionID: "evening", ScenarioID: "night-range", Version: 1, Err: link.ErrDeviceOffline},
		{DeviceID: "tgt-05", SessionID: "evening", ScenarioID: "night-range", Version: 1, Err: errors.New("no room")},
	}
	assigned := decode[SessionAssignment](t, h.call("POST", Prefix+"/sessions/evening/scenario", `{"id": "night-range", "version": 1}`), 200)
	if assigned.Session.Scenario != "night-range" || assigned.Session.ScenarioVersion != 1 || len(assigned.Announcements) != 3 ||
		assigned.Announcements[0].State != AnnouncedTaken || assigned.Announcements[1].State != AnnouncedWaiting ||
		assigned.Announcements[2].State != AnnouncedFailed || assigned.Announcements[2].Error != "no room" {
		t.Fatalf("the assignment answered %+v", assigned)
	}
	// Adding a device asks for its content too, which there was none of.
	if strings.Join(h.link.asked, " ") != "tgt-01/evening tgt-03/evening /evening" {
		t.Errorf("the link was asked %v", h.link.asked)
	}

	// Rated 12: tgt-04, set for 6, cannot join; tgt-05 joins and is
	// announced to; tgt-01 cannot be set below 12 while it plays.
	expectError(t, h.call("POST", Prefix+"/sessions/evening/devices", `{"device_id": "tgt-04"}`), http.StatusConflict, codeAgeRating)
	decode[Session](t, h.call("POST", Prefix+"/sessions/evening/devices", `{"device_id": "tgt-05"}`), 200)
	if strings.Join(h.link.asked, " ") != "tgt-01/evening tgt-03/evening /evening tgt-05/evening" {
		t.Errorf("the link was asked %v", h.link.asked)
	}
	expectError(t, h.call("POST", Prefix+"/devices/tgt-01/min_age", `{"min_age": 6}`), http.StatusConflict, codeAgeRating)

	holdings := decode[DeviceScenarios](t, h.call("GET", Prefix+"/devices/tgt-01/scenarios", ""), 200)
	if holdings.MinAge != 18 || len(holdings.Holdings) != 0 || len(holdings.Assignments) != 1 || holdings.Assignments[0].Installed ||
		holdings.Assignments[0].ScenarioID != "night-range" || holdings.Assignments[0].State != "created" {
		t.Errorf("tgt-01 holds %+v", holdings)
	}
	if err := h.st.RecordDeviceScenarios(ctx, "tgt-01", []store.Holding{{ScenarioID: "night-range", Version: 1}, {ScenarioID: "zombie-alley", Version: 1}}); err != nil {
		t.Fatal(err)
	}
	holdings = decode[DeviceScenarios](t, h.call("GET", Prefix+"/devices/tgt-01/scenarios", ""), 200)
	if len(holdings.Holdings) != 2 || !holdings.Holdings[0].Current || holdings.Holdings[0].Latest != 1 || !holdings.Assignments[0].Installed {
		t.Errorf("tgt-01 holds %+v", holdings)
	}
	detail := decode[ScenarioDetail](t, h.call("GET", Prefix+"/scenarios/night-range", ""), 200)
	if len(detail.Holdings) != 1 || detail.Holdings[0].DeviceID != "tgt-01" || !detail.Holdings[0].Current || detail.Holdings[0].Latest != 1 {
		t.Errorf("the holders of night-range are %+v", detail.Holdings)
	}
	expectError(t, h.call("GET", Prefix+"/devices/tgt-09/scenarios", ""), http.StatusNotFound, codeNotFound)

	decode[Session](t, h.call("POST", Prefix+"/sessions/evening/start", ""), 200)
	expectError(t, h.call("POST", Prefix+"/sessions/evening/scenario", `{"id": "night-range", "version": 1}`), http.StatusConflict, codeBadTransition)
	if !strings.Contains(h.logs.String(), `msg="scenario assigned" component=api session=evening scenario=night-range version=1`) ||
		!strings.Contains(h.logs.String(), `msg="device age set" component=api device=tgt-03 min_age=16`) {
		t.Error("the assignment or the age is not logged")
	}
}
