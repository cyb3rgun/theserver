package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/cyb3rgun/theserver/internal/editor"
	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/mediakind"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The scenario editor (D-041 to D-046): one page per draft with the video,
// the timeline, the zones on a canvas, the property panel from the editor
// field registry, the media panel, validation and publishing. Every action
// is an API v1 request; editor.js draws and sends the changes, HTMX renders
// the forms around it.

// editorData is the editor page.
type editorData struct {
	layout
	Draft     httpapi.Draft
	Name      string // the title of the scenario in the language of the page
	Panel     panelData
	Media     mediaData
	Problems  problemsData
	Script    string
	Words     template.JS
	Shortcuts []shortcut
	Preview   string
	// ZonesMove says the tier of this draft moves zones through time, so the
	// keyframe controls belong on the page. In the video tier they do not
	// exist and a hint stands in their place, instead of a button that
	// writes a keyframe the check then refuses as not_in_tier.
	ZonesMove bool
	Lock      lockData
	History   historyData
}

// historyData is the version list of a draft (D-051). The browser holds its
// own fifty states for undo and redo; this is what the server kept, so a
// person finds their way back after a reload or from another machine.
type historyData struct {
	Versions []historyVersion
	Depth    int
	// Undo and Redo are the texts of the two buttons, so the script needs no
	// text of its own for them.
	List string
	alert
}

type historyVersion struct {
	Version int64
	At      int64
	By      string
	Fields  string
	Restore string
	Confirm string
}

// lockData is the notice about who holds a draft (D-050). Mine is the
// ordinary case: this page took the lock when it opened and refreshes it
// while it stays open. Held with Mine false is somebody else at work, and
// then the page only reads and offers to take the draft over.
type lockData struct {
	Held   bool
	Mine   bool
	Name   string
	Notice string
	// RefreshMs is how often the page knocks, a third of the life of a lock,
	// so two refreshes may be lost before anybody else walks in.
	RefreshMs int64
	Take      string
	Release   string
	Confirm   string
	// At is the stamp of the lock this page holds. The release carries it,
	// so a page that leaves after a newer page of the same person took the
	// lock releases nothing.
	At int64
	alert
}

// panelData is the property panel for one selected object.
type panelData struct {
	Select   string
	Action   string
	Title    string
	Subtitle string
	Nav      []panelGroup
	Fields   []panelField
	// Kind is the group of the selection, so the panel can offer what only
	// that group has, such as the keyframes of a zone.
	Kind string
	// Removable says the selected object can be deleted.
	Removable bool
	// Missing says the selection points at nothing, after a delete.
	Missing bool
	alert
}

// panelGroup is one row of the panel navigation: the parts of the scenario
// of one kind, each with the selection it stands for.
type panelGroup struct {
	Label string
	Links []panelLink
	// Add names the action that makes another one of these, empty when the
	// canvas or the timeline makes them.
	Add     string
	AddText string
}

type panelLink struct {
	Select  string
	Label   string
	Current bool
}

// panelField is one field of the panel, filled from the registry and the
// draft.
type panelField struct {
	ID          string
	Name        string
	Label       string
	Description string
	Why         string
	Control     string
	Unit        string
	Min         string
	Max         string
	Optional    bool
	Value       string
	Languages   []panelText
	On          bool
	Options     []panelOption
	Marks       []panelMark
	Rows        []panelRow
	Problem     string
}

type panelText struct {
	Lang  string
	Label string
	Value string
}

type panelOption struct {
	Value    string
	Label    string
	Selected bool
}

// panelMark is one zone in the zone list of an appearance.
type panelMark struct {
	Value   string
	Label   string
	Checked bool
	// Taken names the other appearance that already holds the zone.
	Taken string
}

// panelRow is one media state with its clip.
type panelRow struct {
	Name    string
	Clip    string
	Options []panelOption
}

// mediaData is the media panel.
type mediaData struct {
	Action string
	Files  []mediaFile
	Limit  int
	alert
}

type mediaFile struct {
	httpapi.DraftMedia
	Use      string
	Size     string
	Duration string
	URL      string
	Used     string // where the manifest uses the file, empty when nowhere
}

// problemsData is the validate panel.
type problemsData struct {
	Problems []editorProblem
	Checked  bool
	Ready    bool
	alert
}

// editorProblem is a problem with the place in the editor it belongs to.
type editorProblem struct {
	problemView
	// Select is the object of the property panel the problem points at.
	Select string
	Label  string
}

type shortcut struct {
	Keys string
	What string
}

func (a *Admin) newEditorData(s session, draft httpapi.Draft) editorData {
	data := editorData{
		layout: a.layout("admin.editor.title", "scenarios", s),
		Draft:  draft,
		Name:   titleIn(s.Lang, draft.Title, draft.ScenarioID),
		Script: a.scripts["editor.js"],
	}
	data.ZonesMove = zonesMove(draft.Tier)
	data.Lock = lockOf(s.Lang, draft)
	data.Title = i18n.T(s.Lang, "admin.editor.title")
	data.Preview = "/admin/editor/" + url.PathEscape(draft.ID) + "/preview"
	data.Shortcuts = shortcutsIn(s.Lang, data.ZonesMove)
	data.Words = editorWords(s.Lang)
	return data
}

// zonesMove is the rule of the validator, seen from the page: a zone carries
// keyframes in the interactive and the layered tier and nowhere else.
func zonesMove(tier string) bool {
	return tier == scenario.TierInteractive || tier == scenario.TierLayered
}

// lockOf is the lock notice of a draft in the language of the page (D-050).
func lockOf(lang string, draft httpapi.Draft) lockData {
	id := url.PathEscape(draft.ID)
	lock := lockData{
		Held:      draft.Lock.Held,
		Mine:      draft.Lock.Mine,
		Name:      draft.Lock.Name,
		RefreshMs: draft.Lock.TTLMs / 3,
		At:        draft.Lock.At,
		Take:      "/admin/editor/" + id + "/lock",
		Release:   "/admin/editor/" + id + "/unlock",
		Confirm:   i18n.T(lang, "admin.editor.confirm_take_over"),
	}
	switch {
	case lock.Mine:
		lock.Notice = i18n.T(lang, "admin.editor.lock_mine")
	case lock.Held:
		lock.Notice = i18n.T(lang, "admin.editor.locked", lock.Name)
	}
	return lock
}

// shortcutsIn lists the keyboard shortcuts of the editor, documented on the
// page itself.
func shortcutsIn(lang string, zonesMove bool) []shortcut {
	keys := []string{"space", "arrows", "shift_arrows", "draw", "finish", "cancel", "remove",
		"keyframe", "keyframe_delete", "appearance", "zoom", "save", "undo", "redo", "validate"}
	if !zonesMove {
		keys = slices.DeleteFunc(keys, func(key string) bool {
			return key == "keyframe" || key == "keyframe_delete"
		})
	}
	out := make([]shortcut, 0, len(keys))
	for _, key := range keys {
		out = append(out, shortcut{
			Keys: i18n.T(lang, "admin.editor.key."+key),
			What: i18n.T(lang, "admin.editor.key."+key+".what"),
		})
	}
	return out
}

// editorWords are the texts editor.js needs, as JSON in a data attribute, so
// the script holds no text of its own.
func editorWords(lang string) template.JS {
	words := map[string]string{}
	for _, key := range []string{"zone", "appearance", "keyframe", "lost"} {
		words[key] = i18n.T(lang, "admin.editor.js."+key)
	}
	encoded, err := json.Marshal(words)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(encoded)
}

// draftOf reads the draft of the request.
func (a *Admin) draftOf(w http.ResponseWriter, r *http.Request, s session) (httpapi.Draft, bool) {
	var draft httpapi.Draft
	err := a.call(r, s, http.MethodGet, "/drafts/"+url.PathEscape(r.PathValue("id")), nil, &draft)
	if err != nil {
		var ae *apiError
		switch {
		case errors.Is(err, errSessionEnded):
			a.sessionEnded(w, r)
		case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
			a.notFound(w, r, s)
		default:
			a.log.Error("the editor could not read a draft", "draft", r.PathValue("id"), "error", err)
			http.Error(w, i18n.T(s.Lang, "admin.error_generic"), http.StatusInternalServerError)
		}
		return httpapi.Draft{}, false
	}
	return draft, true
}

func (a *Admin) notFound(w http.ResponseWriter, r *http.Request, s session) {
	http.Redirect(w, r, "/admin/scenarios?draft_gone=1", http.StatusSeeOther)
}

// editorPage renders the editor for a draft. Opening it takes the lock
// (D-050); when somebody else holds the draft the page reads only and says
// who has it.
func (a *Admin) editorPage(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	if locked, err := a.takeLock(r, s, draft.ID, false); err == nil {
		draft = locked
	} else if !isLocked(err) {
		a.log.Error("the editor could not take a lock", "draft", draft.ID, "error", err)
	}
	data := a.newEditorData(s, draft)
	if r.URL.Query().Get("taken_failed") != "" {
		data.Lock.alert = alert{Error: i18n.T(s.Lang, "admin.editor.take_over_failed")}
	}
	data.Panel = a.panelOf(s, draft, r.URL.Query().Get("select"))
	data.Media = a.mediaOf(s, draft)
	data.History = a.historyOf(r, s, draft)
	data.Problems = problemsData{Problems: editorProblems(s.Lang, draft), Ready: len(draft.Problems) == 0}
	a.render(w, s.Lang, http.StatusOK, "editor", "layout", data)
}

// takeLock asks API v1 for the lock of a draft and answers with the draft as
// it now stands. A refused lock comes back as an apiError with draft_locked,
// which isLocked reads.
func (a *Admin) takeLock(r *http.Request, s session, id string, takeOver bool) (httpapi.Draft, error) {
	var draft httpapi.Draft
	err := a.call(r, s, http.MethodPost, "/drafts/"+url.PathEscape(id)+"/lock",
		httpapi.TakeLock{TakeOver: takeOver}, &draft)
	return draft, err
}

// isLocked says an error is the refusal of a draft somebody else holds.
func isLocked(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Code == "draft_locked"
}

// historyOf reads the versions of a draft from API v1 for the page.
func (a *Admin) historyOf(r *http.Request, s session, draft httpapi.Draft) historyData {
	id := url.PathEscape(draft.ID)
	data := historyData{Depth: store.DraftHistoryDepth, List: "/admin/editor/" + id + "/history"}
	var answer httpapi.DraftHistoryList
	if err := a.call(r, s, http.MethodGet, "/drafts/"+id+"/history", nil, &answer); err != nil {
		if !errors.Is(err, errSessionEnded) {
			a.log.Error("the editor could not read a history", "draft", draft.ID, "error", err)
		}
		data.Error = i18n.T(s.Lang, "admin.error_generic")
		return data
	}
	data.Depth = answer.Depth
	for _, v := range answer.Versions {
		data.Versions = append(data.Versions, historyVersion{
			Version: v.Version, At: v.At, By: v.By,
			Fields:  strings.Join(fieldNames(s.Lang, v.Fields), ", "),
			Restore: "/admin/editor/" + id + "/history/" + strconv.FormatInt(v.Version, 10) + "/restore",
			Confirm: i18n.T(s.Lang, "admin.editor.confirm_restore"),
		})
	}
	return data
}

// fieldNames turns the manifest keys of a change into the group names the
// panel uses, so the history reads like the editor and not like the file.
func fieldNames(lang string, fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if key := "admin.editor.group." + field; i18n.Has(lang, key) {
			out = append(out, i18n.T(lang, key))
			continue
		}
		out = append(out, field)
	}
	return out
}

// editorHistory renders the version list again, which the script asks for
// after every change.
func (a *Admin) editorHistory(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-history", a.historyOf(r, s, draft))
}

// editorRestore writes one version of the history back into the draft and
// renders the version list again.
func (a *Admin) editorRestore(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	var changed httpapi.DraftChanged
	err := a.call(r, s, http.MethodPost,
		"/drafts/"+url.PathEscape(draft.ID)+"/history/"+url.PathEscape(r.PathValue("version"))+"/restore", nil, &changed)
	if err != nil {
		if errors.Is(err, errSessionEnded) {
			a.sessionEnded(w, r)
			return
		}
		data := a.historyOf(r, s, draft)
		data.Error = i18n.T(s.Lang, "admin.editor.restore_failed")
		data.ErrorDetail = detailOf(err)
		a.render(w, s.Lang, http.StatusOK, "editor", "editor-history", data)
		return
	}
	// The manifest behind the canvas is another one now, and the way back is
	// the newest entry of the history, not the undo stack of this browser.
	w.Header().Set("HX-Trigger", "draft-restored")
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-history", a.historyOf(r, s, changed.Draft))
}

// editorLock is the minute refresh of editor.js and the take over button.
// The script wants JSON, the button a page it can land on.
func (a *Admin) editorLock(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	takeOver := r.FormValue("take_over") != ""
	draft, err := a.takeLock(r, s, id, takeOver)
	if !takeOver {
		if err != nil {
			a.editorError(w, r, s, err)
			return
		}
		writeJSON(w, http.StatusOK, draft.Lock)
		return
	}
	if err != nil {
		if errors.Is(err, errSessionEnded) {
			a.sessionEnded(w, r)
			return
		}
		a.log.Error("a draft was not taken over", "draft", id, "error", err)
		http.Redirect(w, r, "/admin/editor/"+url.PathEscape(id)+"?taken_failed=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/editor/"+url.PathEscape(id)+"?taken=1", http.StatusSeeOther)
}

// editorUnlock releases the lock when the page is left. The browser sends
// this with sendBeacon, which is a POST nobody waits for, so the answer is
// an empty 204.
func (a *Admin) editorUnlock(w http.ResponseWriter, r *http.Request, s session) {
	path := "/drafts/" + url.PathEscape(r.PathValue("id")) + "/lock"
	if at := r.URL.Query().Get("at"); at != "" {
		path += "?at=" + url.QueryEscape(at)
	}
	err := a.call(r, s, http.MethodDelete, path, nil, nil)
	if err != nil && !errors.Is(err, errSessionEnded) {
		var ae *apiError
		if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
			a.log.Error("a draft lock was not released", "draft", r.PathValue("id"), "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// previewData is the preview page.
type previewData struct {
	layout
	Draft       httpapi.Draft
	Name        string
	Script      string
	RulesScript string
	Words       template.JS
}

// previewPage plays a draft with the rules of section 7 in the browser
// (D-044).
func (a *Admin) previewPage(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	data := previewData{
		layout:      a.layout("admin.preview.title", "scenarios", s),
		Draft:       draft,
		Name:        titleIn(s.Lang, draft.Title, draft.ScenarioID),
		Script:      a.scripts["preview.js"],
		RulesScript: a.scripts["rules.js"],
		Words:       previewWords(s.Lang),
	}
	a.render(w, s.Lang, http.StatusOK, "preview", "layout", data)
}

// previewWords are the texts preview.js shows; the script holds none.
func previewWords(lang string) template.JS {
	words := map[string]string{}
	for _, key := range []string{"agree", "differ", "ended", "failed"} {
		words[key] = i18n.T(lang, "admin.preview.js."+key)
	}
	encoded, err := json.Marshal(words)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(encoded)
}

// editorTrace hands the shots of a run to the rule engine of the server and
// answers with its trace, which the preview compares with its own.
func (a *Admin) editorTrace(w http.ResponseWriter, r *http.Request, s session) {
	var answer httpapi.TraceOf
	err := a.send(r, s, http.MethodPost, "/drafts/"+url.PathEscape(r.PathValue("id"))+"/trace",
		r.Body, "application/json", &answer)
	if err != nil {
		a.editorError(w, r, s, err)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

// editorDraft hands the draft to editor.js as the API gives it.
func (a *Admin) editorDraft(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

// editorPatch takes a JSON merge patch from editor.js and answers with the
// draft and its problems.
func (a *Admin) editorPatch(w http.ResponseWriter, r *http.Request, s session) {
	var changed httpapi.DraftChanged
	err := a.send(r, s, http.MethodPatch, "/drafts/"+url.PathEscape(r.PathValue("id")), r.Body, "application/json", &changed)
	if err != nil {
		a.editorError(w, r, s, err)
		return
	}
	writeJSON(w, http.StatusOK, changed)
}

// editorError answers a failed request of the script with the translated
// message the page shows.
func (a *Admin) editorError(w http.ResponseWriter, r *http.Request, s session, err error) {
	var ae *apiError
	if errors.Is(err, errSessionEnded) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": i18n.T(s.Lang, "admin.session_ended")})
		return
	}
	message := i18n.T(s.Lang, "admin.error_generic")
	status := http.StatusInternalServerError
	if errors.As(err, &ae) {
		status = ae.Status
		key := "admin.api_error." + ae.Code
		if !i18n.Has(s.Lang, key) {
			key = "admin.api_error.unknown"
		}
		message = i18n.T(s.Lang, key)
		a.log.Debug("the editor was refused", "path", r.URL.Path, "status", ae.Status, "code", ae.Code, "message", ae.Message)
	} else {
		a.log.Error("an editor request failed", "path", r.URL.Path, "error", err)
	}
	writeJSON(w, status, map[string]string{"error": message, "detail": detailOf(err)})
}

func detailOf(err error) string {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Message
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// editorPanel renders the property panel of a selection.
func (a *Admin) editorPanel(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-panel", a.panelOf(s, draft, r.URL.Query().Get("select")))
}

// editorField applies what a person typed in the panel and renders it again.
func (a *Admin) editorField(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderPanelError(w, s, draft, r.FormValue("select"), alert{Error: i18n.T(s.Lang, "admin.error_generic")})
		return
	}
	selection := r.FormValue("select")
	patch, err := formPatch(draft, selection, r.Form)
	if err != nil {
		a.renderPanelError(w, s, draft, selection, alert{
			Error:       i18n.T(s.Lang, "admin.editor.field_refused"),
			ErrorDetail: err.Error(),
		})
		return
	}
	var changed httpapi.DraftChanged
	if err := a.call(r, s, http.MethodPatch, "/drafts/"+url.PathEscape(draft.ID), patch, &changed); err != nil {
		var ae *apiError
		detail := ""
		if errors.As(err, &ae) {
			detail = ae.Message
		}
		if errors.Is(err, errSessionEnded) {
			a.sessionEnded(w, r)
			return
		}
		a.renderPanelError(w, s, draft, selection, alert{
			Error:       i18n.T(s.Lang, "admin.editor.field_refused"),
			ErrorDetail: detail,
		})
		return
	}
	w.Header().Set("HX-Trigger", "draft-changed")
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-panel", a.panelOf(s, changed.Draft, selection))
}

func (a *Admin) renderPanelError(w http.ResponseWriter, s session, draft httpapi.Draft, selection string, problem alert) {
	panel := a.panelOf(s, draft, selection)
	panel.alert = problem
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-panel", panel)
}

// editorMedia renders the media panel.
func (a *Admin) editorMedia(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-media", a.mediaOf(s, draft))
}

// editorUpload hands a media file to the API and renders the media panel.
func (a *Admin) editorUpload(w http.ResponseWriter, r *http.Request, s session) {
	id := url.PathEscape(r.PathValue("id"))
	var changed httpapi.DraftChanged
	err := a.send(r, s, http.MethodPost, "/drafts/"+id+"/media", r.Body, r.Header.Get("Content-Type"), &changed)
	if errors.Is(err, errSessionEnded) {
		a.sessionEnded(w, r)
		return
	}
	if err != nil {
		draft, ok := a.draftOf(w, r, s)
		if !ok {
			return
		}
		media := a.mediaOf(s, draft)
		var ae *apiError
		key := "admin.editor.upload_failed"
		if errors.As(err, &ae) && ae.Code == "bad_media" {
			key = "admin.editor.upload_kind"
		}
		media.alert = alert{Error: i18n.T(s.Lang, key), ErrorDetail: detailOf(err)}
		w.Header().Set("HX-Trigger", "draft-changed")
		a.render(w, s.Lang, http.StatusOK, "editor", "editor-media", media)
		return
	}
	w.Header().Set("HX-Trigger", "draft-changed")
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-media", a.mediaOf(s, changed.Draft))
}

// editorMediaDelete removes one media file.
func (a *Admin) editorMediaDelete(w http.ResponseWriter, r *http.Request, s session) {
	id, name := url.PathEscape(r.PathValue("id")), url.PathEscape(r.PathValue("name"))
	var changed httpapi.DraftChanged
	err := a.call(r, s, http.MethodDelete, "/drafts/"+id+"/media/"+name, nil, &changed)
	if errors.Is(err, errSessionEnded) {
		a.sessionEnded(w, r)
		return
	}
	draft := changed.Draft
	media := mediaData{}
	if err != nil {
		again, ok := a.draftOf(w, r, s)
		if !ok {
			return
		}
		draft = again
		media.alert = alert{Error: i18n.T(s.Lang, "admin.editor.delete_failed"), ErrorDetail: detailOf(err)}
	}
	filled := a.mediaOf(s, draft)
	filled.alert = media.alert
	w.Header().Set("HX-Trigger", "draft-changed")
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-media", filled)
}

// editorMeasure keeps what the browser measured on a clip.
func (a *Admin) editorMeasure(w http.ResponseWriter, r *http.Request, s session) {
	id, name := url.PathEscape(r.PathValue("id")), url.PathEscape(r.PathValue("name"))
	var changed httpapi.DraftChanged
	if err := a.send(r, s, http.MethodPatch, "/drafts/"+id+"/media/"+name, r.Body, "application/json", &changed); err != nil {
		a.editorError(w, r, s, err)
		return
	}
	writeJSON(w, http.StatusOK, changed)
}

// editorFile serves a media file of the draft to the video element.
func (a *Admin) editorFile(w http.ResponseWriter, r *http.Request, s session) {
	id, name := url.PathEscape(r.PathValue("id")), url.PathEscape(r.PathValue("name"))
	a.forward(w, r, s, "/drafts/"+id+"/media/"+name)
}

// editorValidate asks the API what a publish would find.
func (a *Admin) editorValidate(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	var answer httpapi.DraftProblems
	data := problemsData{Checked: true}
	if err := a.call(r, s, http.MethodPost, "/drafts/"+url.PathEscape(draft.ID)+"/validate", nil, &answer); err != nil {
		if errors.Is(err, errSessionEnded) {
			a.sessionEnded(w, r)
			return
		}
		data.alert = alert{Error: i18n.T(s.Lang, "admin.error_generic"), ErrorDetail: detailOf(err)}
	} else {
		draft.Problems = answer.Problems
		data.Problems = editorProblems(s.Lang, draft)
		data.Ready = len(answer.Problems) == 0
	}
	w.Header().Set("HX-Trigger", "draft-changed")
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-problems", data)
}

// editorPublish publishes the draft and goes to the scenario page; a draft
// with problems stays where it is and shows them.
func (a *Admin) editorPublish(w http.ResponseWriter, r *http.Request, s session) {
	draft, ok := a.draftOf(w, r, s)
	if !ok {
		return
	}
	var published httpapi.ScenarioVersion
	err := a.call(r, s, http.MethodPost, "/drafts/"+url.PathEscape(draft.ID)+"/publish", nil, &published)
	if err == nil {
		target := fmt.Sprintf("/admin/scenarios/%s?published=%d", url.PathEscape(published.ID), published.Version)
		w.Header().Set("HX-Redirect", target)
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if errors.Is(err, errSessionEnded) {
		a.sessionEnded(w, r)
		return
	}
	data := problemsData{Checked: true}
	var ae *apiError
	if errors.As(err, &ae) && len(ae.Problems) > 0 {
		draft.Problems = ae.Problems
		data.Problems = editorProblems(s.Lang, draft)
	}
	data.alert = alert{Error: i18n.T(s.Lang, "admin.editor.publish_refused"), ErrorDetail: detailOf(err)}
	w.Header().Set("HX-Trigger", "draft-changed")
	a.render(w, s.Lang, http.StatusOK, "editor", "editor-problems", data)
}

// editorDelete throws a draft away.
func (a *Admin) editorDelete(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	err := a.call(r, s, http.MethodDelete, "/drafts/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, errSessionEnded) {
		a.sessionEnded(w, r)
		return
	}
	if err != nil {
		a.log.Error("a draft could not be deleted", "draft", id, "error", err)
	}
	http.Redirect(w, r, "/admin/scenarios?draft_deleted=1", http.StatusSeeOther)
}

// newDraft opens a draft from the catalogue: empty for an id and a tier, or
// as a copy of a published version.
func (a *Admin) newDraft(w http.ResponseWriter, r *http.Request, s session) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/scenarios", http.StatusSeeOther)
		return
	}
	body := httpapi.NewDraft{
		ID:         strings.TrimSpace(r.FormValue("id")),
		Tier:       r.FormValue("tier"),
		TargetType: strings.TrimSpace(r.FormValue("target_type")),
	}
	if title := strings.TrimSpace(r.FormValue("title")); title != "" {
		body.Title = map[string]string{}
		for _, lang := range i18n.Languages() {
			body.Title[lang] = title
		}
	}
	if from := r.FormValue("from"); from != "" {
		id, version, ok := parseChoice(from)
		if ok {
			body.From = &httpapi.FromVersion{ID: id, Version: version}
			body.ID = ""
		}
	}
	var draft httpapi.Draft
	if err := a.call(r, s, http.MethodPost, "/drafts", body, &draft); err != nil {
		if errors.Is(err, errSessionEnded) {
			a.sessionEnded(w, r)
			return
		}
		data := a.newScenariosData(s)
		a.failed(w, r, err, &data.alert)
		if !a.loadCatalogue(w, r, s, &data) {
			return
		}
		a.render(w, s.Lang, http.StatusBadRequest, "scenarios", "layout", data)
		return
	}
	http.Redirect(w, r, "/admin/editor/"+url.PathEscape(draft.ID), http.StatusSeeOther)
}

// selectionOf splits a selection into its group and its index: "zone:2" is
// the third zone, "rules" the rules of the scenario.
func selectionOf(selection string) (string, int) {
	group, index, found := strings.Cut(selection, ":")
	if !found {
		return group, -1
	}
	n, err := strconv.Atoi(index)
	if err != nil {
		return group, -1
	}
	return group, n
}

// panelOf builds the property panel of a selection from the field registry
// and the draft (D-046).
func (a *Admin) panelOf(s session, draft httpapi.Draft, selection string) panelData {
	group, index := selectionOf(selection)
	if !slices.Contains(editor.Groups(), group) {
		group, index, selection = editor.GroupScenario, -1, editor.GroupScenario
	}
	data := panelData{
		Select: selection,
		Action: "/admin/editor/" + url.PathEscape(draft.ID) + "/field",
		Kind:   group,
		Title:  i18n.T(s.Lang, "admin.editor.group."+group),
	}
	manifest := manifestMap(draft)
	data.Nav = navOf(s.Lang, manifest, selection)
	if indexed(group) {
		list := listAt(manifest, group)
		if index < 0 || index >= len(list) {
			data.Missing = true
			return data
		}
		data.Removable = true
		data.Subtitle = objectName(s.Lang, group, list, index)
	}
	var problems []editorProblem
	for _, p := range editorProblems(s.Lang, draft) {
		if p.Select == selection {
			problems = append(problems, p)
		}
	}
	for _, f := range editor.Of(group) {
		data.Fields = append(data.Fields, a.panelField(s, draft, manifest, f, index, problems))
	}
	return data
}

// plainField drops the indexes of a field path, so that the problem
// zone[2].points_value meets the field zone[#].points_value.
func plainField(field string) string {
	var b strings.Builder
	for {
		before, rest, found := strings.Cut(field, "[")
		b.WriteString(before)
		if !found {
			return b.String()
		}
		_, field, _ = strings.Cut(rest, "]")
	}
}

// problemOf finds the problem that belongs to a field, if there is one.
func problemOf(problems []editorProblem, key string) string {
	want := plainField(key)
	for _, p := range problems {
		got := plainField(p.Field)
		if got == want || strings.HasPrefix(got, want+".") {
			return p.Text
		}
	}
	return ""
}

// indexed says a group holds a list of objects.
func indexed(group string) bool {
	switch group {
	case editor.GroupZone, editor.GroupAppearance, editor.GroupFollowup:
		return true
	}
	return false
}

// listAt is the list of objects a group holds: zones and appearances stand
// at the top of the manifest, follow ups below the reaction.
func listAt(manifest map[string]any, group string) []any {
	switch group {
	case editor.GroupZone:
		list, _ := manifest["zone"].([]any)
		return list
	case editor.GroupAppearance:
		list, _ := manifest["appearance"].([]any)
		return list
	case editor.GroupFollowup:
		reaction, _ := manifest["reaction"].(map[string]any)
		list, _ := reaction["followup"].([]any)
		return list
	}
	return nil
}

// setList writes a list back after a removal.
func setList(manifest map[string]any, group string, list []any) {
	switch group {
	case editor.GroupZone:
		manifest["zone"] = list
	case editor.GroupAppearance:
		manifest["appearance"] = list
	case editor.GroupFollowup:
		reaction, _ := manifest["reaction"].(map[string]any)
		if reaction == nil {
			reaction = map[string]any{}
			manifest["reaction"] = reaction
		}
		reaction["followup"] = list
	}
}

// navOf lists every object of the scenario with the selection it stands for.
func navOf(lang string, manifest map[string]any, selection string) []panelGroup {
	groups := []panelGroup{{Label: i18n.T(lang, "admin.editor.parts")}}
	for _, group := range []string{editor.GroupScenario, editor.GroupDisplay, editor.GroupRules,
		editor.GroupMedia, editor.GroupImmediate} {
		groups[0].Links = append(groups[0].Links, panelLink{
			Select: group, Label: i18n.T(lang, "admin.editor.group."+group), Current: selection == group,
		})
	}
	for _, group := range []string{editor.GroupZone, editor.GroupAppearance, editor.GroupFollowup} {
		row := panelGroup{Label: i18n.T(lang, "admin.editor.group."+group)}
		for i := range listAt(manifest, group) {
			what := fmt.Sprintf("%s:%d", group, i)
			row.Links = append(row.Links, panelLink{
				Select: what, Label: objectName(lang, group, listAt(manifest, group), i), Current: selection == what,
			})
		}
		switch group {
		case editor.GroupAppearance:
			row.Add, row.AddText = "appearance", i18n.T(lang, "admin.editor.appearance_add")
		case editor.GroupFollowup:
			row.Add, row.AddText = "followup", i18n.T(lang, "admin.editor.followup_add")
		}
		groups = append(groups, row)
	}
	return groups
}

// objectName names the selected object of a list for the panel head.
func objectName(lang string, group string, list []any, index int) string {
	object, _ := list[index].(map[string]any)
	switch group {
	case editor.GroupFollowup:
		return fmt.Sprint(object["appearance"], " ", object["zone_class"])
	default:
		if id, ok := object["id"].(string); ok {
			return id
		}
	}
	return i18n.T(lang, "admin.editor.unnamed")
}

// manifestMap decodes the manifest of a draft as plain JSON, which the panel
// reads values from and the form writes them back into.
func manifestMap(draft httpapi.Draft) map[string]any {
	m := map[string]any{}
	if len(draft.Manifest) > 0 {
		json.Unmarshal(draft.Manifest, &m)
	}
	return m
}

// valueAt reads the value of a field key from a manifest map; index is the
// place in the list of a repeated object.
func valueAt(m map[string]any, key string, index int) any {
	current := any(m)
	for _, part := range strings.Split(key, ".") {
		name, isList := strings.CutSuffix(part, "[#]")
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[name]
		if isList {
			list, ok := current.([]any)
			if !ok || index < 0 || index >= len(list) {
				return nil
			}
			current = list[index]
		}
	}
	return current
}

// setAt writes a value into a manifest map, making the objects on the way.
func setAt(m map[string]any, key string, index int, value any) {
	parts := strings.Split(key, ".")
	current := m
	for i, part := range parts {
		name, isList := strings.CutSuffix(part, "[#]")
		if i == len(parts)-1 && !isList {
			current[name] = value
			return
		}
		next := current[name]
		if isList {
			list, ok := next.([]any)
			if !ok || index < 0 || index >= len(list) {
				return
			}
			object, ok := list[index].(map[string]any)
			if !ok {
				object = map[string]any{}
				list[index] = object
			}
			current = object
			continue
		}
		object, ok := next.(map[string]any)
		if !ok {
			object = map[string]any{}
			current[name] = object
		}
		current = object
	}
}

func text(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprint(v)
	}
}

// panelField fills one field of the panel from the registry and the draft.
func (a *Admin) panelField(s session, draft httpapi.Draft, manifest map[string]any, f editor.Field, index int, problems []editorProblem) panelField {
	texts := f.In(s.Lang)
	value := valueAt(manifest, f.Key, index)
	out := panelField{
		ID: f.ID(), Name: f.Key, Label: texts.Label, Description: texts.Description, Why: texts.Why,
		Control: f.Control, Optional: f.Optional, Value: text(value), Problem: problemOf(problems, f.Key),
	}
	if f.Unit != "" {
		out.Unit = i18n.T(s.Lang, "admin.editor.unit."+f.Unit)
	}
	if f.Min != nil {
		out.Min = strconv.Itoa(*f.Min)
	}
	if f.Max != nil {
		out.Max = strconv.Itoa(*f.Max)
	}
	switch f.Control {
	case editor.ControlSwitch:
		on, _ := value.(bool)
		out.On = on
	case editor.ControlTexts:
		object, _ := value.(map[string]any)
		for _, lang := range i18n.Languages() {
			out.Languages = append(out.Languages, panelText{
				Lang:  lang,
				Label: i18n.T(s.Lang, "admin.language."+lang),
				Value: text(object[lang]),
			})
		}
	case editor.ControlSelect:
		out.Options = a.optionsOf(s, draft, manifest, f, out.Value)
	case editor.ControlFixed:
		if f.Name() == "shape" && out.Value != "" {
			out.Value = i18n.T(s.Lang, "admin.editor.option.shape."+out.Value)
		}
	case editor.ControlZones:
		out.Marks = zoneMarks(s.Lang, manifest, index)
	case editor.ControlStates:
		out.Rows = stateRows(s.Lang, draft, manifest)
	}
	return out
}

// optionsOf fills a select: from the fixed list of the field, or from what
// the draft holds.
func (a *Admin) optionsOf(s session, draft httpapi.Draft, manifest map[string]any, f editor.Field, value string) []panelOption {
	var options []panelOption
	add := func(v, label string) {
		options = append(options, panelOption{Value: v, Label: label, Selected: v == value})
	}
	if f.Optional || f.From != "" {
		add("", i18n.T(s.Lang, "admin.editor.none"))
	}
	switch {
	case len(f.Enum) > 0:
		for _, v := range f.Enum {
			label := v
			if key := "admin.editor.option." + f.Name() + "." + v; i18n.Has(s.Lang, key) {
				label = i18n.T(s.Lang, key)
			} else if f.Name() == "age_rating" {
				label = i18n.T(s.Lang, "admin.age."+v)
			}
			add(v, label)
		}
	case f.From == editor.FromStates:
		for _, state := range stateNames(manifest) {
			add(state, state)
		}
	case f.From == editor.FromAppearances:
		list, _ := manifest["appearance"].([]any)
		for _, item := range list {
			object, _ := item.(map[string]any)
			if id, ok := object["id"].(string); ok {
				add(id, id)
			}
		}
	case f.From == editor.FromThen:
		add("end", i18n.T(s.Lang, "admin.editor.option.then.end"))
		add("next", i18n.T(s.Lang, "admin.editor.option.then.next"))
		for _, state := range stateNames(manifest) {
			add("back:"+state, i18n.T(s.Lang, "admin.editor.option.then.back", state))
		}
	default:
		for _, file := range draft.Media {
			if file.Use == useOf(f.From) {
				add(file.Path, file.Name)
			}
		}
	}
	// A value the draft no longer offers is kept, so nothing disappears.
	if value != "" && !slices.ContainsFunc(options, func(o panelOption) bool { return o.Value == value }) {
		add(value, value+" ("+i18n.T(s.Lang, "admin.editor.missing")+")")
	}
	return options
}

// useOf maps the list of a select to the use of a media file (D-045).
func useOf(from string) string {
	switch from {
	case editor.FromClips:
		return mediakind.UseClip
	case editor.FromSounds:
		return mediakind.UseSound
	case editor.FromOverlays:
		return mediakind.UseOverlay
	}
	return ""
}

// stateNames lists the media states of a draft, in order.
func stateNames(manifest map[string]any) []string {
	media, _ := manifest["media"].(map[string]any)
	states, _ := media["state"].(map[string]any)
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// zoneMarks lists every zone with a mark for the ones of the appearance, and
// says which other appearance holds a zone.
func zoneMarks(lang string, manifest map[string]any, index int) []panelMark {
	appearances, _ := manifest["appearance"].([]any)
	owner := map[string]string{}
	var mine []string
	for i, item := range appearances {
		object, _ := item.(map[string]any)
		id, _ := object["id"].(string)
		zones, _ := object["zones"].([]any)
		for _, z := range zones {
			name, _ := z.(string)
			if i == index {
				mine = append(mine, name)
			} else {
				owner[name] = id
			}
		}
	}
	zones, _ := manifest["zone"].([]any)
	marks := make([]panelMark, 0, len(zones))
	for _, item := range zones {
		object, _ := item.(map[string]any)
		id, _ := object["id"].(string)
		label := id
		if name, ok := object["name"].(map[string]any); ok {
			if in := text(name[lang]); in != "" {
				label = in + " (" + id + ")"
			}
		}
		marks = append(marks, panelMark{Value: id, Label: label, Checked: slices.Contains(mine, id), Taken: owner[id]})
	}
	return marks
}

// stateRows lists the media states with their clips, and one empty row to
// add another.
func stateRows(lang string, draft httpapi.Draft, manifest map[string]any) []panelRow {
	media, _ := manifest["media"].(map[string]any)
	states, _ := media["state"].(map[string]any)
	clips := func(selected string) []panelOption {
		options := []panelOption{{Value: "", Label: i18n.T(lang, "admin.editor.none"), Selected: selected == ""}}
		for _, file := range draft.Media {
			if file.Use == mediakind.UseClip {
				options = append(options, panelOption{Value: file.Path, Label: file.Name, Selected: file.Path == selected})
			}
		}
		return options
	}
	rows := []panelRow{}
	for _, name := range stateNames(manifest) {
		clip := text(states[name])
		rows = append(rows, panelRow{Name: name, Clip: clip, Options: clips(clip)})
	}
	return append(rows, panelRow{Options: clips("")})
}

// formPatch turns what the panel form carries into a JSON merge patch on the
// manifest.
func formPatch(draft httpapi.Draft, selection string, form url.Values) (map[string]any, error) {
	group, index := selectionOf(selection)
	if !slices.Contains(editor.Groups(), group) {
		return nil, fmt.Errorf("unknown selection %q", selection)
	}
	manifest := manifestMap(draft)
	if form.Get("remove") != "" {
		return removePatch(manifest, group, index)
	}
	for _, f := range editor.Of(group) {
		if f.Control == editor.ControlFixed {
			continue
		}
		value, err := formValue(f, form)
		if err != nil {
			return nil, err
		}
		setAt(manifest, f.Key, index, value)
	}
	if group == editor.GroupMedia {
		states, err := formStates(form)
		if err != nil {
			return nil, err
		}
		media, _ := manifest["media"].(map[string]any)
		if media == nil {
			media = map[string]any{}
			manifest["media"] = media
		}
		// A state that is gone is removed with null, as a merge patch does.
		old, _ := media["state"].(map[string]any)
		for name := range old {
			if _, ok := states[name]; !ok {
				states[name] = nil
			}
		}
		media["state"] = states
	}
	return patchOf(manifest, group), nil
}

// removePatch takes an object out of its list. A zone that goes also
// leaves the appearance that named it, so nothing points at what is gone.
func removePatch(manifest map[string]any, group string, index int) (map[string]any, error) {
	list := listAt(manifest, group)
	if !indexed(group) || index < 0 || index >= len(list) {
		return nil, fmt.Errorf("there is nothing to delete at %s %d", group, index)
	}
	gone, _ := list[index].(map[string]any)
	setList(manifest, group, append(append([]any{}, list[:index]...), list[index+1:]...))
	if group != editor.GroupZone {
		return patchOf(manifest, group), nil
	}
	id, _ := gone["id"].(string)
	appearances := listAt(manifest, editor.GroupAppearance)
	for _, item := range appearances {
		object, _ := item.(map[string]any)
		zones, _ := object["zones"].([]any)
		kept := make([]any, 0, len(zones))
		for _, z := range zones {
			if name, _ := z.(string); name != id {
				kept = append(kept, z)
			}
		}
		object["zones"] = kept
	}
	return map[string]any{"zone": manifest["zone"], "appearance": appearances}, nil
}

// patchOf takes the part of the manifest a group lives in.
func patchOf(manifest map[string]any, group string) map[string]any {
	switch group {
	case editor.GroupZone:
		return map[string]any{"zone": manifest["zone"]}
	case editor.GroupAppearance:
		return map[string]any{"appearance": manifest["appearance"]}
	case editor.GroupImmediate, editor.GroupFollowup:
		return map[string]any{"reaction": manifest["reaction"]}
	default:
		return map[string]any{group: manifest[group]}
	}
}

// formValue reads one field from the form in the shape the model wants.
func formValue(f editor.Field, form url.Values) (any, error) {
	name := f.Key
	switch f.Control {
	case editor.ControlSwitch:
		return form.Get(name) == "true", nil
	case editor.ControlNumber:
		raw := strings.TrimSpace(form.Get(name))
		if raw == "" {
			if f.Optional {
				return nil, nil
			}
			return float64(0), nil
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a whole number", f.Key, raw)
		}
		if f.Min != nil && value < *f.Min || f.Max != nil && value > *f.Max {
			return nil, fmt.Errorf("%s: %d is outside the range", f.Key, value)
		}
		return float64(value), nil
	case editor.ControlTexts:
		out := map[string]any{}
		empty := true
		for _, lang := range i18n.Languages() {
			value := strings.TrimSpace(form.Get(name + "." + lang))
			out[lang] = value
			empty = empty && value == ""
		}
		if empty && f.Optional {
			return nil, nil
		}
		return out, nil
	case editor.ControlZones:
		values := form[name]
		list := make([]any, 0, len(values))
		for _, v := range values {
			list = append(list, v)
		}
		return list, nil
	case editor.ControlStates:
		return nil, nil // formStates reads the rows
	default:
		return strings.TrimSpace(form.Get(name)), nil
	}
}

// formStates reads the rows of the media state table.
func formStates(form url.Values) (map[string]any, error) {
	names, clips := form["state.name"], form["state.clip"]
	out := map[string]any{}
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		clip := ""
		if i < len(clips) {
			clip = clips[i]
		}
		if !scenario.ValidID(name) {
			return nil, fmt.Errorf("media state %q: lower case letters, digits, hyphens and underscores", name)
		}
		out[name] = clip
	}
	return out, nil
}

// mediaOf builds the media panel.
func (a *Admin) mediaOf(s session, draft httpapi.Draft) mediaData {
	data := mediaData{Action: "/admin/editor/" + url.PathEscape(draft.ID) + "/media"}
	used := usedFiles(draft)
	for _, file := range draft.Media {
		entry := mediaFile{
			DraftMedia: file,
			Use:        i18n.T(s.Lang, "admin.editor.use."+useName(file.Use)),
			Size:       sizeIn(s.Lang, file.Size),
			URL:        "/admin/editor/" + url.PathEscape(draft.ID) + "/file/" + url.PathEscape(file.Name),
			Used:       used[file.Path],
		}
		if file.DurationMs > 0 {
			entry.Duration = i18n.T(s.Lang, "admin.editor.seconds",
				strconv.FormatFloat(float64(file.DurationMs)/1000, 'f', 1, 64))
		}
		data.Files = append(data.Files, entry)
	}
	return data
}

func useName(use string) string {
	if use == "" {
		return "none"
	}
	return use
}

// usedFiles maps every media path the manifest names to where it is used.
func usedFiles(draft httpapi.Draft) map[string]string {
	out := map[string]string{}
	manifest := manifestMap(draft)
	media, _ := manifest["media"].(map[string]any)
	if main := text(media["main"]); main != "" {
		out[main] = "media.main"
	}
	states, _ := media["state"].(map[string]any)
	for name, clip := range states {
		out[text(clip)] = "media.state." + name
	}
	reaction, _ := manifest["reaction"].(map[string]any)
	immediate, _ := reaction["immediate"].(map[string]any)
	for _, key := range []string{"impact_sound", "blood"} {
		if path := text(immediate[key]); path != "" {
			out[path] = "reaction.immediate." + key
		}
	}
	return out
}

// editorProblems translates the problems of a draft and points each at the
// object of the panel it belongs to.
func editorProblems(lang string, draft httpapi.Draft) []editorProblem {
	manifest := manifestMap(draft)
	out := make([]editorProblem, 0, len(draft.Problems))
	for _, p := range draft.Problems {
		view := problemView{Code: p.Code, Field: p.Field, Text: i18n.T(lang, "scenario.problem."+p.Code), Detail: p.Detail}
		item := editorProblem{problemView: view}
		item.Select, item.Label = placeOf(lang, manifest, p.Field)
		out = append(out, item)
	}
	return out
}

// placeOf maps the field of a problem to a selection of the panel and a
// label a person can read.
func placeOf(lang string, manifest map[string]any, field string) (string, string) {
	head, rest, _ := strings.Cut(field, ".")
	name, indexText, isIndexed := strings.Cut(head, "[")
	index := -1
	if isIndexed {
		index, _ = strconv.Atoi(strings.TrimSuffix(indexText, "]"))
	}
	group := ""
	switch name {
	case "zone":
		group = editor.GroupZone
	case "appearance":
		group = editor.GroupAppearance
	case "scenario":
		group = editor.GroupScenario
	case "display":
		group = editor.GroupDisplay
	case "rules":
		group = editor.GroupRules
	case "media", "files":
		group = editor.GroupMedia
	case "reaction":
		group = editor.GroupImmediate
		if strings.HasPrefix(rest, "followup[") {
			group = editor.GroupFollowup
			inner, _, _ := strings.Cut(strings.TrimPrefix(rest, "followup["), "]")
			index, _ = strconv.Atoi(inner)
		}
	}
	if group == "" {
		return "", field
	}
	label := i18n.T(lang, "admin.editor.group."+group)
	if index >= 0 {
		list := listAt(manifest, group)
		if index < len(list) {
			label += " " + objectName(lang, group, list, index)
		}
		return fmt.Sprintf("%s:%d", group, index), label
	}
	return group, label
}
