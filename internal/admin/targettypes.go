package admin

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/targettype"
)

// The target type pages (D-065): the list with the form that describes a new
// type, and one page per type with its fields from the registry, the outline
// of its beacon layout, the rectangle a device of this type is told, and the
// devices that are set to it. The outline is structure, not design: the
// picture as a rectangle, the beacon frame as a second one, a dot per
// cluster.

type targetTypesData struct {
	layout
	Types  []targetTypeRow
	Fields []typeField
}

// targetTypeRow is one line of the list.
type targetTypeRow struct {
	httpapi.TargetType
	Label   string
	Devices int
}

type targetTypeData struct {
	layout
	Type     httpapi.TargetType
	Label    string
	Note     string
	Fields   []typeField
	Outline  outline
	Calib    httpapi.Calibration
	HasCalib bool
	Script   string // integrity of static/scenarios.js, which asks before a delete
}

// typeField is one field of the form, filled from the registry and the type.
type typeField struct {
	Key         string
	ID          string
	Control     string
	Label       string
	Description string
	Why         string
	Unit        string
	Enum        []string
	Options     []selectOption
	Value       string
	Texts       []langValue
	Beacons     []beaconRow
	Min         string
	Max         string
	Step        string
	Optional    bool
	Fixed       bool
}

// selectOption is one value of a select with its word in the language of
// the page.
type selectOption struct {
	Value string
	Label string
}

// langValue is one language of a text field.
type langValue struct {
	Lang  string
	Name  string
	Label string
	Value string
}

// beaconRow is one cluster in the form; the last row is empty, so a cluster
// can be added without a script.
type beaconRow struct {
	Index int
	X     string
	Y     string
	Ch    string
}

// outline is the drawing of a beacon layout, in millimetres. Every value is
// already a number the template writes into the SVG.
type outline struct {
	ViewBox string
	// The picture, whose top left corner is the origin of the layout.
	W, H float64
	// The frame the clusters span; HasFrame is false without one.
	HasFrame                bool
	FrameX, FrameY          float64
	FrameW, FrameH          float64
	Beacons                 []outlinePoint
	Dot, Stroke, DashLength float64
}

type outlinePoint struct {
	X, Y float64
}

// textIn reads a text of the type in the language of the page.
func textIn(texts map[string]string, lang string) string {
	if texts == nil {
		return ""
	}
	if text, ok := texts[lang]; ok && text != "" {
		return text
	}
	return texts[i18n.Fallback]
}

func (a *Admin) targetTypesPage(w http.ResponseWriter, r *http.Request, s session) {
	data := targetTypesData{layout: a.layout("admin.target_types.title", "target-types", s)}
	if !a.loadTargetTypes(w, r, s, &data) {
		return
	}
	a.render(w, s.Lang, http.StatusOK, "target-types", "layout", data)
}

func (a *Admin) loadTargetTypes(w http.ResponseWriter, r *http.Request, s session, data *targetTypesData) bool {
	var list struct {
		TargetTypes []httpapi.TargetType `json:"target_types"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/target-types", nil, &list), &data.alert) {
		return false
	}
	data.Types = make([]targetTypeRow, 0, len(list.TargetTypes))
	for _, t := range list.TargetTypes {
		data.Types = append(data.Types, targetTypeRow{
			TargetType: t, Label: textIn(t.Name, s.Lang), Devices: len(t.Devices),
		})
	}
	if data.Fields == nil {
		data.Fields = fieldsOf(httpapi.TargetType{Class: "pi", Orientation: "landscape", Sound: "none"}, s.Lang, true)
	}
	return true
}

// createTargetType describes a new type from the form of the list page.
func (a *Admin) createTargetType(w http.ResponseWriter, r *http.Request, s session) {
	data := targetTypesData{layout: a.layout("admin.target_types.title", "target-types", s)}
	body, err := targetTypeForm(r)
	if err != nil {
		data.Error = i18n.T(s.Lang, "admin.target_types.bad_number")
		data.Fields = fieldsOf(body, s.Lang, true)
		if a.loadTargetTypes(w, r, s, &data) {
			a.render(w, s.Lang, http.StatusBadRequest, "target-types", "layout", data)
		}
		return
	}
	var created httpapi.TargetType
	callErr := a.call(r, s, http.MethodPost, "/target-types", body, &created)
	if a.failed(w, r, callErr, &data.alert) {
		return
	}
	if data.Error != "" {
		data.Fields = fieldsOf(body, s.Lang, true)
		if a.loadTargetTypes(w, r, s, &data) {
			a.render(w, s.Lang, http.StatusBadRequest, "target-types", "layout", data)
		}
		return
	}
	http.Redirect(w, r, "/admin/target-types/"+url.PathEscape(created.ID)+"?created=1", http.StatusSeeOther)
}

func (a *Admin) targetTypePage(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	data, ok := a.loadTargetType(w, r, s, id)
	if !ok {
		return
	}
	switch q := r.URL.Query(); {
	case q.Has("created"):
		data.Notice = i18n.T(s.Lang, "admin.target_types.created", data.Label)
	case q.Has("saved"):
		data.Notice = i18n.T(s.Lang, "admin.target_types.saved", data.Label)
	}
	a.render(w, s.Lang, http.StatusOK, "target-type", "layout", data)
}

func (a *Admin) loadTargetType(w http.ResponseWriter, r *http.Request, s session, id string) (targetTypeData, bool) {
	data := targetTypeData{
		layout: a.layout("admin.target_types.one", "target-types", s),
		Script: a.scripts["scenarios.js"],
	}
	data.Back = "/admin/target-types/" + url.PathEscape(id)
	path := "/target-types/" + url.PathEscape(id)
	err := a.call(r, s, http.MethodGet, path, nil, &data.Type)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		data.alert = alert{Error: i18n.T(s.Lang, "admin.target_types.unknown", id)}
		a.render(w, s.Lang, http.StatusNotFound, "target-type", "layout", data)
		return data, false
	}
	if a.failed(w, r, err, &data.alert) {
		return data, false
	}
	data.Label = textIn(data.Type.Name, s.Lang)
	data.Note = textIn(data.Type.Notes, s.Lang)
	data.Title = data.Label
	data.Fields = fieldsOf(data.Type, s.Lang, false)
	data.Outline = outlineOf(data.Type)

	// The rectangle a device of this type is told; a layout that spans no
	// area has none, which the page says instead of a number (D-063).
	calibErr := a.call(r, s, http.MethodGet, path+"/calibration", nil, &data.Calib)
	switch {
	case calibErr == nil:
		data.HasCalib = true
	case errors.As(calibErr, &ae) && ae.Status == http.StatusConflict:
	default:
		if a.failed(w, r, calibErr, &data.alert) {
			return data, false
		}
	}
	return data, true
}

// saveTargetType writes the form of the type page.
func (a *Admin) saveTargetType(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	body, formErr := targetTypeForm(r)
	body.ID = id
	var callErr error
	if formErr == nil {
		var saved httpapi.TargetType
		callErr = a.call(r, s, http.MethodPut, "/target-types/"+url.PathEscape(id), body, &saved)
	}
	data, ok := a.loadTargetType(w, r, s, id)
	if !ok {
		return
	}
	if a.failed(w, r, callErr, &data.alert) {
		return
	}
	if formErr != nil {
		data.Error = i18n.T(s.Lang, "admin.target_types.bad_number")
	}
	if data.Error == "" {
		http.Redirect(w, r, "/admin/target-types/"+url.PathEscape(id)+"?saved=1", http.StatusSeeOther)
		return
	}
	// The page comes back with what was typed, so nothing has to be typed
	// again.
	data.Fields = fieldsOf(body, s.Lang, false)
	a.render(w, s.Lang, http.StatusBadRequest, "target-type", "layout", data)
}

// deleteTargetType removes a type; a builtin one and one that devices are
// set to are refused, and the page says why (D-064).
func (a *Admin) deleteTargetType(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	err := a.call(r, s, http.MethodDelete, "/target-types/"+url.PathEscape(id), nil, nil)
	if err == nil {
		http.Redirect(w, r, "/admin/target-types?deleted="+url.QueryEscape(id), http.StatusSeeOther)
		return
	}
	data, ok := a.loadTargetType(w, r, s, id)
	if !ok {
		return
	}
	if a.failed(w, r, err, &data.alert) {
		return
	}
	a.render(w, s.Lang, http.StatusConflict, "target-type", "layout", data)
}

// setDeviceTargetType says on the device page which type a device is; the
// device is calibrated at once (D-063).
func (a *Admin) setDeviceTargetType(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	body := httpapi.DeviceTargetType{TargetType: strings.TrimSpace(r.PostFormValue("target_type"))}
	var answer httpapi.DeviceCalibrated
	err := a.call(r, s, http.MethodPost, "/devices/"+url.PathEscape(id)+"/target-type", body, &answer)
	if errors.Is(err, errSessionEnded) {
		a.sessionEnded(w, r)
		return
	}
	data, ok := a.loadDevice(w, r, s, id)
	if !ok {
		return
	}
	if a.failed(w, r, err, &data.alert) {
		return
	}
	if data.Error == "" {
		switch {
		case body.TargetType == "":
			data.Notice = i18n.T(s.Lang, "admin.device.target_type_cleared", id)
		case answer.Calibration != nil:
			data.Notice = i18n.T(s.Lang, "admin.device.target_type_done", id, body.TargetType, answer.Calibration.Value)
		default:
			data.Notice = i18n.T(s.Lang, "admin.device.target_type_plain", id, body.TargetType)
		}
	}
	a.render(w, s.Lang, http.StatusOK, "device", "layout", data)
}

// targetTypeForm reads the form of the create or the save page. A number
// that cannot be read gives an error, and the type comes back as it was
// typed so nothing is lost.
func targetTypeForm(r *http.Request) (httpapi.TargetType, error) {
	if err := r.ParseForm(); err != nil {
		return httpapi.TargetType{}, err
	}
	out := httpapi.TargetType{
		ID:          strings.TrimSpace(r.PostFormValue("id")),
		Class:       r.PostFormValue("class"),
		Orientation: r.PostFormValue("orientation"),
		Sound:       r.PostFormValue("sound"),
		Name:        map[string]string{},
		Notes:       map[string]string{},
		Beacons:     []httpapi.Beacon{},
	}
	for _, lang := range i18n.Languages() {
		if text := strings.TrimSpace(r.PostFormValue("name." + lang)); text != "" {
			out.Name[lang] = text
		}
		if text := strings.TrimSpace(r.PostFormValue("notes." + lang)); text != "" {
			out.Notes[lang] = text
		}
	}
	var err error
	out.DisplayWMM, err = number(r, "display_w_mm", err)
	out.DisplayHMM, err = number(r, "display_h_mm", err)
	width, err := number(r, "res_w", err)
	height, err := number(r, "res_h", err)
	out.ResW, out.ResH = int(width), int(height)

	xs, ys, chs := r.PostForm["beacon.x"], r.PostForm["beacon.y"], r.PostForm["beacon.ch"]
	for i := range max(len(xs), len(ys)) {
		x, y, ch := "", "", ""
		if i < len(xs) {
			x = strings.TrimSpace(xs[i])
		}
		if i < len(ys) {
			y = strings.TrimSpace(ys[i])
		}
		if i < len(chs) {
			ch = strings.TrimSpace(chs[i])
		}
		if x == "" && y == "" {
			// An empty row is a cluster that was removed, or the row that
			// is there to add one.
			continue
		}
		bx, bErr := strconv.ParseFloat(orZero(x), 64)
		by, bErr2 := strconv.ParseFloat(orZero(y), 64)
		channel, bErr3 := strconv.Atoi(orZero(ch))
		if err == nil {
			err = errors.Join(bErr, bErr2, bErr3)
		}
		out.Beacons = append(out.Beacons, httpapi.Beacon{X: bx, Y: by, Ch: channel})
	}
	return out, err
}

// number reads one number of the form and keeps the first error.
func number(r *http.Request, key string, err error) (float64, error) {
	text := strings.TrimSpace(r.PostFormValue(key))
	if text == "" {
		return 0, err
	}
	value, parseErr := strconv.ParseFloat(text, 64)
	if parseErr != nil && err == nil {
		return 0, fmt.Errorf("%s: %w", key, parseErr)
	}
	return value, err
}

func orZero(text string) string {
	if text == "" {
		return "0"
	}
	return text
}

// fieldsOf fills the form from the registry and the type. withID shows the
// id field, which only a new type has.
func fieldsOf(t httpapi.TargetType, lang string, withID bool) []typeField {
	var out []typeField
	for _, f := range targettype.Fields() {
		if f.Key == "id" && !withID {
			continue
		}
		texts := f.In(lang)
		field := typeField{
			Key: f.Key, ID: f.ID(), Control: f.Control, Unit: f.Unit, Enum: f.Enum,
			Label: texts.Label, Description: texts.Description, Why: texts.Why,
			Optional: f.Optional, Step: "1",
		}
		for _, value := range f.Enum {
			field.Options = append(field.Options, selectOption{
				Value: value, Label: i18n.T(lang, "admin."+f.Key+"."+value),
			})
		}
		if f.Decimals {
			field.Step = "any"
		}
		if f.Min != nil {
			field.Min = trimNumber(*f.Min)
		}
		if f.Max != nil {
			field.Max = trimNumber(*f.Max)
		}
		switch f.Key {
		case "id":
			field.Value = t.ID
		case "class":
			field.Value = t.Class
		case "orientation":
			field.Value = t.Orientation
		case "sound":
			field.Value = t.Sound
		case "display_w_mm":
			field.Value = trimNumber(t.DisplayWMM)
		case "display_h_mm":
			field.Value = trimNumber(t.DisplayHMM)
		case "res_w":
			field.Value = strconv.Itoa(t.ResW)
		case "res_h":
			field.Value = strconv.Itoa(t.ResH)
		case "name":
			field.Texts = textRows("name", t.Name, lang)
		case "notes":
			field.Texts = textRows("notes", t.Notes, lang)
		case "beacons":
			field.Beacons = beaconRows(t.Beacons)
		}
		out = append(out, field)
	}
	return out
}

func textRows(key string, texts map[string]string, lang string) []langValue {
	var out []langValue
	for _, l := range i18n.Languages() {
		out = append(out, langValue{
			Lang: l, Name: key + "." + l, Label: i18n.T(lang, "admin.language."+l), Value: texts[l],
		})
	}
	return out
}

// beaconRows are the clusters of a type plus one empty row, so the next
// cluster can be typed without a script.
func beaconRows(beacons []httpapi.Beacon) []beaconRow {
	out := make([]beaconRow, 0, len(beacons)+1)
	for i, b := range beacons {
		out = append(out, beaconRow{
			Index: i + 1, X: trimNumber(b.X), Y: trimNumber(b.Y), Ch: strconv.Itoa(b.Ch),
		})
	}
	// The empty row adds a cluster; its channel is the next free number.
	return append(out, beaconRow{Index: len(beacons) + 1, Ch: strconv.Itoa(len(beacons))})
}

// trimNumber writes a number the way a form takes it back: no exponent, no
// trailing zeros.
func trimNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// outlineOf draws the layout of a type: the picture as a rectangle with its
// top left corner at the origin, the frame the clusters span, and a dot per
// cluster. It is structure, not design (D-065).
func outlineOf(t httpapi.TargetType) outline {
	out := outline{W: t.DisplayWMM, H: t.DisplayHMM}
	if out.W <= 0 || out.H <= 0 {
		return outline{}
	}
	minX, minY, maxX, maxY := 0.0, 0.0, out.W, out.H
	for i, b := range t.Beacons {
		out.Beacons = append(out.Beacons, outlinePoint{X: b.X, Y: b.Y})
		if i == 0 {
			out.FrameX, out.FrameY = b.X, b.Y
			out.FrameW, out.FrameH = b.X, b.Y
		}
		out.FrameX, out.FrameY = math.Min(out.FrameX, b.X), math.Min(out.FrameY, b.Y)
		out.FrameW, out.FrameH = math.Max(out.FrameW, b.X), math.Max(out.FrameH, b.Y)
		minX, minY = math.Min(minX, b.X), math.Min(minY, b.Y)
		maxX, maxY = math.Max(maxX, b.X), math.Max(maxY, b.Y)
	}
	if len(t.Beacons) > 1 {
		out.FrameW, out.FrameH = out.FrameW-out.FrameX, out.FrameH-out.FrameY
		out.HasFrame = out.FrameW > 0 && out.FrameH > 0
	}
	margin := math.Max(maxX-minX, maxY-minY) / 20
	minX, minY = minX-margin, minY-margin
	width, height := (maxX-minX)+margin, (maxY-minY)+margin
	out.ViewBox = strings.Join([]string{
		trimNumber(round2(minX)), trimNumber(round2(minY)),
		trimNumber(round2(width)), trimNumber(round2(height)),
	}, " ")
	out.Stroke = round2(math.Max(width, height) / 150)
	out.Dot = round2(math.Max(width, height) / 60)
	out.DashLength = round2(out.Stroke * 4)
	return out
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
