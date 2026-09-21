package admin

import (
	"context"
	"maps"
	"net/url"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/store"
)

// The list, the outline and the forms of the target type pages (D-065).
func TestTargetTypePagesAndActions(t *testing.T) {
	h := newHarness(t)

	page := h.html("GET", "/admin/target-types", nil)
	contains(t, page, "Target types", "board-10", "bar-12", "tv-50",
		"Bar display 11.9 inch", "ships with the server", `id="new-target-type"`,
		// The form comes from the registry, with help at every field.
		`name="display_w_mm"`, `name="beacon.x"`, "Picture width", "Beacon clusters")

	// The detail draws the layout as structure: the picture, the frame the
	// clusters span and a dot per cluster.
	detail := h.html("GET", "/admin/target-types/bar-12", nil)
	contains(t, detail, "<svg class=\"layout\"", "stroke-dasharray", "<circle", "1480 x 320 px", "295 x 64 mm",
		"-138,-340 1618,-340 1618,660 -138,660", `id="edit-target-type"`)
	if strings.Count(detail, "<circle") != 4 {
		t.Errorf("the outline of bar-12 has %d dots, want 4", strings.Count(detail, "<circle"))
	}

	// A type is described in the form of the list page and opens as its own
	// page.
	form := url.Values{
		"id": {"stand-7"}, "name.en": {"Phone stand"}, "name.de": {"Handyhalter"},
		"class": {"pi"}, "display_w_mm": {"150"}, "display_h_mm": {"70"},
		"res_w": {"1080"}, "res_h": {"1920"}, "orientation": {"portrait"}, "sound": {"usb"},
		"beacon.x": {"0", "150", "150", "0", ""}, "beacon.y": {"0", "0", "70", "70", ""},
		"notes.en": {"A phone in a stand."}, "notes.de": {"Ein Telefon im Halter."},
	}
	created := h.do("POST", "/admin/target-types", form, true)
	if created.Code != 303 || created.Header().Get("Location") != "/admin/target-types/stand-7?created=1" {
		t.Fatalf("the create answered %d to %q", created.Code, created.Header().Get("Location"))
	}
	own := h.html("GET", "/admin/target-types/stand-7?created=1", nil)
	contains(t, own, "The target type Phone stand is created.", "Phone stand", "A phone in a stand.", "1080 x 1920 px")

	// A number that cannot be read keeps the page and says so.
	bad := maps.Clone(form)
	bad.Set("display_w_mm", "wide")
	refused := h.do("POST", "/admin/target-types/stand-7", bad, true)
	if refused.Code != 400 {
		t.Fatalf("a bad number answered %d", refused.Code)
	}
	contains(t, refused.Body.String(), "A number could not be read")

	// A change is saved and shows on the page; the row that was empty is
	// a cluster now.
	saved := maps.Clone(form)
	saved.Set("sound", "none")
	saved["beacon.x"] = []string{"0", "150", "150", "0", "20"}
	saved["beacon.y"] = []string{"0", "0", "70", "70", "35"}
	if rec := h.do("POST", "/admin/target-types/stand-7", saved, true); rec.Code != 303 {
		t.Fatalf("the save answered %d: %s", rec.Code, rec.Body.String())
	}
	after := h.html("GET", "/admin/target-types/stand-7?saved=1", nil)
	contains(t, after, "is saved.", "None")
	if strings.Count(after, "<circle") != 5 {
		t.Errorf("after the save the outline has %d dots, want 5", strings.Count(after, "<circle"))
	}

	// A builtin type cannot be deleted, and the page says why.
	builtin := h.do("POST", "/admin/target-types/bar-12/delete", url.Values{}, true)
	if builtin.Code != 409 {
		t.Fatalf("deleting a builtin type answered %d", builtin.Code)
	}
	contains(t, builtin.Body.String(), "A target type that ships with the server cannot be deleted.")

	// A type that a device is set to cannot be deleted either.
	h.device("tgt-01", store.StatusApproved)
	h.html("POST", "/admin/devices/tgt-01/target-type", url.Values{"target_type": {"stand-7"}})
	used := h.do("POST", "/admin/target-types/stand-7/delete", url.Values{}, true)
	if used.Code != 409 {
		t.Fatalf("deleting a used type answered %d", used.Code)
	}
	contains(t, used.Body.String(), "Devices are set to this target type.")

	// The device lets it go, then the type is gone.
	h.html("POST", "/admin/devices/tgt-01/target-type", url.Values{"target_type": {""}})
	gone := h.do("POST", "/admin/target-types/stand-7/delete", url.Values{}, true)
	if gone.Code != 303 || gone.Header().Get("Location") != "/admin/target-types?deleted=stand-7" {
		t.Fatalf("the delete answered %d to %q", gone.Code, gone.Header().Get("Location"))
	}
	if list := h.html("GET", "/admin/target-types", nil); strings.Contains(list, "stand-7") {
		t.Error("the deleted type is still in the list")
	}
	if rec := h.do("GET", "/admin/target-types/stand-7", nil, true); rec.Code != 404 {
		t.Errorf("the page of the deleted type answered %d", rec.Code)
	}
}

// The device page says which type a device is, lets an operator set it and
// says which rectangle the device was told (D-063, D-065).
func TestDevicePageSetsTheTargetType(t *testing.T) {
	h := newLinkedHarness(t)
	h.device("tgt-01", store.StatusApproved)

	page := h.html("GET", "/admin/devices/tgt-01/view", nil)
	contains(t, page, "Target type", `id="target-type-form"`, `value="bar-12"`, "Bar display 11.9 inch")

	set := h.html("POST", "/admin/devices/tgt-01/target-type", url.Values{"target_type": {"bar-12"}})
	contains(t, set, "Device tgt-01 is a bar-12 and was told the rectangle -138,-340 1618,-340 1618,660 -138,660.")
	device, err := h.st.GetDevice(context.Background(), "tgt-01")
	if err != nil || device.TargetType != "bar-12" {
		t.Fatalf("the device is %+v, %v", device, err)
	}
	contains(t, h.html("GET", "/admin/devices", nil), `href="/admin/target-types/bar-12"`)

	cleared := h.html("POST", "/admin/devices/tgt-01/target-type", url.Values{"target_type": {""}})
	contains(t, cleared, "Device tgt-01 has no target type any more.")

	// A type the server does not have is refused and the page says so.
	unknown := h.do("POST", "/admin/devices/tgt-01/target-type", url.Values{"target_type": {"nothing"}}, true)
	contains(t, unknown.Body.String(), "Not found.")
}

// A draft made for a type starts with the canvas of that type (D-062).
func TestNewDraftFromATargetType(t *testing.T) {
	h := newHarness(t)
	contains(t, h.html("GET", "/admin/scenarios", nil), `id="new-target-type"`, `name="target_type"`,
		"no type, default canvas", "Bar display 11.9 inch")

	rec := h.do("POST", "/admin/editor", url.Values{
		"id": {"bar-test"}, "tier": {"video"}, "target_type": {"bar-12"},
	}, true)
	if rec.Code != 303 {
		t.Fatalf("the new draft answered %d: %s", rec.Code, rec.Body.String())
	}
	draft := strings.TrimPrefix(rec.Header().Get("Location"), "/admin/editor/")
	panel := h.html("GET", "/admin/editor/"+draft+"/panel?select=display", nil)
	contains(t, panel, "1480", "320")
}
