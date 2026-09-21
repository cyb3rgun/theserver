// Package targettype holds the fields of a target type (D-061): every field
// an operator edits, how the page shows it, and its label, its short
// description and the longer why in every language. The settings registry
// and the editor registry have the same shape, and the admin pages render
// all three with one help component.
package targettype

import (
	"slices"
	"strings"

	"github.com/cyb3rgun/theserver/internal/store"
)

// The controls a field is shown with.
const (
	// ControlText is one line of text.
	ControlText = "text"
	// ControlTexts is one line per language of the catalogue.
	ControlTexts = "texts"
	// ControlNumber is a number; Decimals says whether it may have any.
	ControlNumber = "number"
	// ControlSelect is one value out of Enum.
	ControlSelect = "select"
	// ControlBeacons is the table of beacon clusters, two numbers per row.
	ControlBeacons = "beacons"
)

// A Field is one field of a target type.
type Field struct {
	// Key is the field in the API shape of a target type.
	Key string
	// Control is how the page shows the field.
	Control string
	// Enum is the fixed list of a select.
	Enum []string
	// Unit is what the number counts, as the page writes it.
	Unit string
	// Min and Max bound a number; nil means open.
	Min *float64
	Max *float64
	// Decimals says a number may have a fraction, as millimetres do.
	Decimals bool
	// Optional says an empty value is allowed.
	Optional bool
	// Text is the label, the description and the why, by language.
	Text map[string]Texts
}

// Texts are the three texts of a field in one language: the label at the
// field, the description on hover, the why below.
type Texts struct {
	Label       string
	Description string
	Why         string
}

func n(v float64) *float64 { return &v }

// Fields lists every field of a target type, in the order the form shows
// them.
func Fields() []Field {
	return slices.Clone(registry)
}

// Get finds a field by its key.
func Get(key string) (Field, bool) {
	for _, f := range registry {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// In returns the texts of a field in lang, falling back to English.
func (f Field) In(lang string) Texts {
	if t, ok := f.Text[lang]; ok && t.Label != "" {
		return t
	}
	return f.Text["en"]
}

// ID is the key in a form that an html id takes.
func (f Field) ID() string {
	return "tt-" + strings.ReplaceAll(f.Key, "_", "-")
}

var registry = []Field{
	{
		Key: "id", Control: ControlText,
		Text: map[string]Texts{
			"en": {
				Label:       "Type id",
				Description: "The short name the server keeps the type under, set once.",
				Why:         "Devices, scenarios and clients name the type by this id. It holds lower case letters, digits, hyphens and underscores, and it cannot be changed afterwards: a type with another id is another type.",
			},
			"de": {
				Label:       "Typ-Kennung",
				Description: "Der kurze Name, unter dem der Server den Typ führt, einmal vergeben.",
				Why:         "Geräte, Szenarien und Clients nennen den Typ über diese Kennung. Sie enthält kleine Buchstaben, Ziffern, Bindestriche und Unterstriche und lässt sich später nicht ändern: Ein Typ mit anderer Kennung ist ein anderer Typ.",
			},
		},
	},
	{
		Key: "name", Control: ControlTexts,
		Text: map[string]Texts{
			"en": {
				Label:       "Name",
				Description: "The name people read on the pages, in every language.",
				Why:         "It stands in the type list, on the device page and in the editor. English is needed; a language that is missing falls back to it.",
			},
			"de": {
				Label:       "Name",
				Description: "Der Name, den Menschen auf den Seiten lesen, in jeder Sprache.",
				Why:         "Er steht in der Typliste, auf der Geräteseite und im Editor. Englisch wird gebraucht; eine fehlende Sprache fällt darauf zurück.",
			},
		},
	},
	{
		Key: "class", Control: ControlSelect, Enum: []string{store.ClassESP, store.ClassPi, store.ClassPC},
		Text: map[string]Texts{
			"en": {
				Label:       "Class",
				Description: "The computer that drives the target: a board, a Raspberry Pi or a PC.",
				Why:         "The class says what a target of this kind can carry. It is the same class a device reports when it connects, so a device whose class differs from its type is worth a second look.",
			},
			"de": {
				Label:       "Klasse",
				Description: "Der Rechner hinter dem Ziel: eine Platine, ein Raspberry Pi oder ein PC.",
				Why:         "Die Klasse sagt, was ein Ziel dieser Art tragen kann. Es ist dieselbe Klasse, die ein Gerät beim Verbinden meldet; ein Gerät, dessen Klasse von seinem Typ abweicht, lohnt einen zweiten Blick.",
			},
		},
	},
	{
		Key: "display_w_mm", Control: ControlNumber, Unit: "mm", Min: n(1), Max: n(10000), Decimals: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Picture width",
				Description: "The width of the picture on the display, in millimetres.",
				Why:         "The picture size turns the beacon positions into pixels: a millimetre is as many pixels as the resolution divided by this size. Measure the picture, not the housing.",
			},
			"de": {
				Label:       "Bildbreite",
				Description: "Die Breite des Bildes auf dem Display, in Millimetern.",
				Why:         "Die Bildgröße rechnet die Bakenpositionen in Pixel um: Ein Millimeter sind so viele Pixel wie die Auflösung geteilt durch diese Größe. Messen Sie das Bild, nicht das Gehäuse.",
			},
		},
	},
	{
		Key: "display_h_mm", Control: ControlNumber, Unit: "mm", Min: n(1), Max: n(10000), Decimals: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Picture height",
				Description: "The height of the picture on the display, in millimetres.",
				Why:         "It does for the vertical what the width does for the horizontal. A wrong height tilts every shot up or down the further it lands from the middle.",
			},
			"de": {
				Label:       "Bildhöhe",
				Description: "Die Höhe des Bildes auf dem Display, in Millimetern.",
				Why:         "Sie tut für die Senkrechte, was die Breite für die Waagerechte tut. Eine falsche Höhe verschiebt jeden Schuss nach oben oder unten, je weiter er von der Mitte entfernt landet.",
			},
		},
	},
	{
		Key: "res_w", Control: ControlNumber, Unit: "px", Min: n(1), Max: n(16384),
		Text: map[string]Texts{
			"en": {
				Label:       "Resolution across",
				Description: "The width of the picture in pixels, which is the canvas of a scenario.",
				Why:         "A scenario made for this type draws on a canvas of this size, and the beacon rectangle is given in it. Take the resolution the target really runs at, not the one the datasheet promises.",
			},
			"de": {
				Label:       "Auflösung waagerecht",
				Description: "Die Breite des Bildes in Pixeln, die Zeichenfläche eines Szenarios.",
				Why:         "Ein Szenario für diesen Typ zeichnet auf einer Fläche dieser Größe, und das Bakenrechteck wird darin angegeben. Nehmen Sie die Auflösung, mit der das Ziel wirklich läuft, nicht die des Datenblatts.",
			},
		},
	},
	{
		Key: "res_h", Control: ControlNumber, Unit: "px", Min: n(1), Max: n(16384),
		Text: map[string]Texts{
			"en": {
				Label:       "Resolution down",
				Description: "The height of the picture in pixels.",
				Why:         "Together with the width it is the canvas a scenario of this type is drawn on. A draft opened from this type starts with exactly this canvas.",
			},
			"de": {
				Label:       "Auflösung senkrecht",
				Description: "Die Höhe des Bildes in Pixeln.",
				Why:         "Zusammen mit der Breite ist sie die Fläche, auf der ein Szenario dieses Typs gezeichnet wird. Ein Entwurf, der von diesem Typ ausgeht, beginnt genau mit dieser Fläche.",
			},
		},
	},
	{
		Key: "orientation", Control: ControlSelect, Enum: []string{store.Landscape, store.Portrait},
		Text: map[string]Texts{
			"en": {
				Label:       "Orientation",
				Description: "How the target hangs: wider than tall, or taller than wide.",
				Why:         "A scenario carries the orientation of the type it was made for. It says how the picture is meant to be seen, not how the pixels are counted: the resolution stays as the display reports it.",
			},
			"de": {
				Label:       "Ausrichtung",
				Description: "Wie das Ziel hängt: breiter als hoch oder höher als breit.",
				Why:         "Ein Szenario trägt die Ausrichtung des Typs, für den es gemacht wurde. Sie sagt, wie das Bild gesehen werden soll, nicht wie die Pixel gezählt werden: Die Auflösung bleibt so, wie das Display sie meldet.",
			},
		},
	},
	{
		Key: "sound", Control: ControlSelect,
		Enum: []string{store.SoundNone, store.SoundBuiltin, store.SoundHDMI, store.SoundUSB},
		Text: map[string]Texts{
			"en": {
				Label:       "Sound",
				Description: "Where a target of this kind makes its sound.",
				Why:         "A scenario with sound needs a target that has one. The entry says whether the sound comes out of the display over HDMI, out of a speaker on the board, out of a USB device, or not at all.",
			},
			"de": {
				Label:       "Ton",
				Description: "Woher ein Ziel dieser Art seinen Ton nimmt.",
				Why:         "Ein Szenario mit Ton braucht ein Ziel, das einen hat. Der Eintrag sagt, ob der Ton über HDMI aus dem Display kommt, aus einem Lautsprecher auf der Platine, aus einem USB-Gerät oder gar nicht.",
			},
		},
	},
	{
		Key: "beacons", Control: ControlBeacons, Unit: "mm", Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Beacon clusters",
				Description: "Where the clusters sit, in millimetres from the top left corner of the picture.",
				Why:         "The pistol sees the clusters and reports where it aimed inside their frame; the server turns that frame into canvas coordinates and sends it to the target. A cluster may sit outside the picture, so a value may be negative. Four clusters around the picture are the usual layout. An empty row is dropped, and a row that is filled is added.",
			},
			"de": {
				Label:       "Bakencluster",
				Description: "Wo die Cluster sitzen, in Millimetern von der linken oberen Bildecke.",
				Why:         "Die Pistole sieht die Cluster und meldet, wohin sie innerhalb ihres Rahmens gezielt hat; der Server rechnet diesen Rahmen in Zeichenflächen-Koordinaten um und schickt ihn an das Ziel. Ein Cluster darf außerhalb des Bildes sitzen, ein Wert also negativ sein. Vier Cluster um das Bild herum sind der übliche Aufbau. Eine leere Zeile entfällt, eine ausgefüllte kommt hinzu.",
			},
		},
	},
	{
		Key: "notes", Control: ControlTexts, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Notes",
				Description: "What the next person should know about this kind of target.",
				Why:         "Where the clusters are screwed on, which cable the sound takes, what breaks first. It may stay empty, but it is the only place where such a thing is written down.",
			},
			"de": {
				Label:       "Notizen",
				Description: "Was die nächste Person über diese Art Ziel wissen sollte.",
				Why:         "Wo die Cluster angeschraubt sind, über welches Kabel der Ton läuft, was zuerst kaputtgeht. Darf leer bleiben, ist aber die einzige Stelle, an der so etwas festgehalten wird.",
			},
		},
	},
}
