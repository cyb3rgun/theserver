// Package editor holds the fields of the scenario editor (D-046): every
// field a person edits, where it lives in the manifest, how the page shows
// it, and its label, its short description and the longer why in every
// language. The settings registry has the same shape, and the admin pages
// render both with one help component.
package editor

import (
	"slices"
	"strings"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The controls a field is shown with.
const (
	// ControlText is one line of text.
	ControlText = "text"
	// ControlTexts is one line per language of the catalogue.
	ControlTexts = "texts"
	// ControlNumber is a whole number.
	ControlNumber = "number"
	// ControlSwitch is on or off.
	ControlSwitch = "switch"
	// ControlSelect is one value out of a list, from Enum or from From.
	ControlSelect = "select"
	// ControlZones is the list of zones of the scenario, with a mark per zone.
	ControlZones = "zones"
	// ControlStates is the table of media states and their clips (INTERACTIVE).
	ControlStates = "states"
	// ControlFixed is shown but not changed here; the drawing or the draft sets it.
	ControlFixed = "fixed"
)

// The lists a Select can draw its values from, beside a fixed Enum.
const (
	// FromStates are the media states of the draft.
	FromStates = "states"
	// FromClips, FromSounds and FromOverlays are the media files of the
	// draft that fit the use (D-045).
	FromClips    = "clips"
	FromSounds   = "sounds"
	FromOverlays = "overlays"
	// FromAppearances are the appearances of the scenario.
	FromAppearances = "appearances"
	// FromThen are end, next and back to each media state.
	FromThen = "then"
)

// The groups of fields, which are the objects the property panel shows.
const (
	GroupScenario   = "scenario"
	GroupDisplay    = "display"
	GroupRules      = "rules"
	GroupZone       = "zone"
	GroupAppearance = "appearance"
	GroupImmediate  = "immediate"
	GroupFollowup   = "followup"
	GroupMedia      = "media"
)

// A Field is one field of the editor.
type Field struct {
	// Key is where the field lives in the manifest, as the editor addresses
	// it: the json path, with [#] for the index of a repeated object.
	Key string
	// Group is the object the field belongs to.
	Group string
	// Control is how the page shows the field.
	Control string
	// Enum is the fixed list of a Select, From the list it takes from the
	// draft; a field has one or the other.
	Enum []string
	From string
	// Unit is what the number counts, already translated by the page.
	Unit string
	// Min and Max bound a number; nil means open.
	Min *int
	Max *int
	// Optional says an empty value is allowed and means something of its
	// own, as an empty points value means the value from the rules.
	Optional bool
	// Text is the label, the description and the why, by language.
	Text map[string]Texts
}

// Texts are the three texts of a field in one language, as the settings have
// them: the label at the field, the description on hover, the why below.
type Texts struct {
	Label       string
	Description string
	Why         string
}

func n(v int) *int { return &v }

// Fields lists every field of the editor, in the order the panel shows them.
func Fields() []Field {
	return slices.Clone(registry)
}

// Groups lists the groups in the order of the panel.
func Groups() []string {
	return []string{GroupScenario, GroupDisplay, GroupRules, GroupZone, GroupAppearance,
		GroupImmediate, GroupFollowup, GroupMedia}
}

// Of lists the fields of one group.
func Of(group string) []Field {
	var out []Field
	for _, f := range registry {
		if f.Group == group {
			out = append(out, f)
		}
	}
	return out
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

// Name is the last part of the key, which names the field in the manifest.
func (f Field) Name() string {
	if i := strings.LastIndex(f.Key, "."); i >= 0 {
		return f.Key[i+1:]
	}
	return f.Key
}

// ID is the key in a form that an html id and a form field take.
func (f Field) ID() string {
	return strings.NewReplacer("[#]", "", ".", "-").Replace(f.Key)
}

var registry = []Field{
	// The scenario itself.
	{
		Key: "scenario.id", Group: GroupScenario, Control: ControlFixed,
		Text: map[string]Texts{
			"en": {
				Label:       "Scenario id",
				Description: "The name the package carries; it is set when the draft is opened.",
				Why:         "Targets keep a scenario under this id, and every new version keeps it. It cannot be changed here: a scenario with another id is another scenario, so open a new draft for it.",
			},
			"de": {
				Label:       "Szenario-Kennung",
				Description: "Der Name, den das Paket trägt; er wird beim Anlegen des Entwurfs gesetzt.",
				Why:         "Geräte behalten ein Szenario unter dieser Kennung, und jede neue Version behält sie. Sie lässt sich hier nicht ändern: Ein Szenario mit anderer Kennung ist ein anderes Szenario, dafür legen Sie einen neuen Entwurf an.",
			},
		},
	},
	{
		Key: "scenario.title", Group: GroupScenario, Control: ControlTexts,
		Text: map[string]Texts{
			"en": {
				Label:       "Title",
				Description: "The name people read in the catalogue, in every language.",
				Why:         "The title stands on the scenario page, in the session and on the target. Both languages are needed, else the package does not validate.",
			},
			"de": {
				Label:       "Titel",
				Description: "Der Name, den Menschen im Katalog lesen, in jeder Sprache.",
				Why:         "Der Titel steht auf der Szenarioseite, in der Sitzung und am Ziel. Beide Sprachen werden gebraucht, sonst ist das Paket nicht gültig.",
			},
		},
	},
	{
		Key: "scenario.description", Group: GroupScenario, Control: ControlTexts, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Description",
				Description: "One or two sentences about what happens in the scenario.",
				Why:         "It helps the person at the counter pick the right scenario. It may stay empty, but then it stays empty in both languages.",
			},
			"de": {
				Label:       "Beschreibung",
				Description: "Ein bis zwei Sätze darüber, was im Szenario passiert.",
				Why:         "Sie hilft der Person am Tresen, das passende Szenario zu wählen. Sie darf leer bleiben, dann aber in beiden Sprachen.",
			},
		},
	},
	{
		Key: "scenario.age_rating", Group: GroupScenario, Control: ControlSelect, Enum: scenario.AgeRatings(),
		Text: map[string]Texts{
			"en": {
				Label:       "Age rating",
				Description: "The age from which this scenario may be played.",
				Why:         "theserver refuses to give a session a scenario rated above the age a device of that session is set for. Rate honestly: the rating is the only thing that keeps a hard scenario off a device set for children.",
			},
			"de": {
				Label:       "Altersfreigabe",
				Description: "Das Alter, ab dem dieses Szenario gespielt werden darf.",
				Why:         "theserver weist ein Szenario ab, das höher eingestuft ist als das Alter, auf das ein Gerät der Sitzung eingestellt ist. Stufen Sie ehrlich ein: Die Freigabe ist das Einzige, was ein hartes Szenario von einem Gerät für Kinder fernhält.",
			},
		},
	},
	{
		Key: "scenario.duration_s", Group: GroupScenario, Control: ControlNumber, Unit: "s", Min: n(1),
		Text: map[string]Texts{
			"en": {
				Label:       "Duration",
				Description: "How long the scenario runs, in seconds.",
				Why:         "The timeline and every time window end here. The editor sets it from the main video when you upload one; a shorter duration cuts appearances that lie behind it, which validation then names.",
			},
			"de": {
				Label:       "Dauer",
				Description: "Wie lange das Szenario läuft, in Sekunden.",
				Why:         "Die Zeitleiste und jedes Zeitfenster enden hier. Der Editor setzt die Dauer aus dem Hauptvideo, sobald Sie eines hochladen; eine kürzere Dauer schneidet Auftritte ab, die dahinter liegen, was die Prüfung dann benennt.",
			},
		},
	},
	{
		Key: "scenario.author", Group: GroupScenario, Control: ControlText, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Author",
				Description: "Who made this scenario.",
				Why:         "It stands in the package and on the scenario page, so a venue knows whom to ask about it. The editor fills in the admin who opened the draft.",
			},
			"de": {
				Label:       "Urheber",
				Description: "Wer dieses Szenario gemacht hat.",
				Why:         "Der Eintrag steht im Paket und auf der Szenarioseite, damit eine Anlage weiß, wen sie dazu fragen kann. Der Editor trägt den Admin ein, der den Entwurf angelegt hat.",
			},
		},
	},
	{
		Key: "scenario.licence", Group: GroupScenario, Control: ControlSelect, Enum: scenario.Licences(),
		Text: map[string]Texts{
			"en": {
				Label:       "Licence",
				Description: "Whether the scenario is official, from the community, or your own.",
				Why:         "Scenarios made in your venue are private. Official marks packages from CYB3RGUN, which will carry a signature later; community marks what someone shares.",
			},
			"de": {
				Label:       "Lizenz",
				Description: "Ob das Szenario offiziell, aus der Gemeinschaft oder Ihr eigenes ist.",
				Why:         "In Ihrer Anlage gebaute Szenarien sind privat. Offiziell kennzeichnet Pakete von CYB3RGUN, die später eine Signatur tragen; Gemeinschaft kennzeichnet, was jemand teilt.",
			},
		},
	},

	// The canvas the zones live on.
	{
		Key: "display.canvas.w", Group: GroupDisplay, Control: ControlNumber, Unit: "px", Min: n(1),
		Text: map[string]Texts{
			"en": {
				Label:       "Canvas width",
				Description: "The width of the coordinate space the zones are drawn in.",
				Why:         "Zones are kept in these coordinates, not in the pixels of your screen, so the same scenario fits every target. The editor sets it from the video you upload.",
			},
			"de": {
				Label:       "Flächenbreite",
				Description: "Die Breite des Koordinatenraums, in dem die Zonen liegen.",
				Why:         "Zonen werden in diesen Koordinaten gespeichert, nicht in den Bildpunkten Ihres Bildschirms, damit dasselbe Szenario auf jedes Ziel passt. Der Editor setzt sie aus dem hochgeladenen Video.",
			},
		},
	},
	{
		Key: "display.canvas.h", Group: GroupDisplay, Control: ControlNumber, Unit: "px", Min: n(1),
		Text: map[string]Texts{
			"en": {
				Label:       "Canvas height",
				Description: "The height of the coordinate space the zones are drawn in.",
				Why:         "Together with the width it gives the picture its shape. A zone outside this space is refused at validation.",
			},
			"de": {
				Label:       "Flächenhöhe",
				Description: "Die Höhe des Koordinatenraums, in dem die Zonen liegen.",
				Why:         "Zusammen mit der Breite ergibt sie das Format des Bildes. Eine Zone außerhalb dieser Fläche wird bei der Prüfung abgewiesen.",
			},
		},
	},
	{
		Key: "display.orientation", Group: GroupDisplay, Control: ControlSelect, Enum: scenario.Orientations(),
		Text: map[string]Texts{
			"en": {
				Label:       "Orientation",
				Description: "Whether the target stands upright or lies wide.",
				Why:         "A target mounted upright plays portrait scenarios without black bars. It follows the canvas you set; change it only when the target is mounted the other way.",
			},
			"de": {
				Label:       "Ausrichtung",
				Description: "Ob das Ziel hochkant steht oder quer liegt.",
				Why:         "Ein hochkant montiertes Ziel spielt Hochformat-Szenarien ohne schwarze Balken. Die Ausrichtung folgt der gesetzten Fläche; ändern Sie sie nur, wenn das Ziel anders montiert ist.",
			},
		},
	},
	{
		Key: "display.fit", Group: GroupDisplay, Control: ControlSelect, Enum: scenario.Fits(),
		Text: map[string]Texts{
			"en": {
				Label:       "Fit",
				Description: "How the picture fills a screen of another shape: cover, contain or fill.",
				Why:         "Cover fills the screen and cuts the edges, contain shows everything with bars, fill stretches the picture. Cover keeps the zones where the eye expects them as long as the shapes are close.",
			},
			"de": {
				Label:       "Einpassung",
				Description: "Wie das Bild einen anders geformten Schirm füllt: füllen, einpassen oder dehnen.",
				Why:         "Füllen deckt den Schirm und schneidet die Ränder ab, Einpassen zeigt alles mit Balken, Dehnen zieht das Bild. Füllen hält die Zonen dort, wo das Auge sie erwartet, solange die Formate ähnlich sind.",
			},
		},
	},

	// The rules of the game.
	{
		Key: "rules.points_per_hit_default", Group: GroupRules, Control: ControlNumber, Min: n(0),
		Text: map[string]Texts{
			"en": {
				Label:       "Points per hit",
				Description: "What a hit is worth when the zone says nothing of its own.",
				Why:         "Every zone can carry its own value; this is the value for all the others. A head zone usually carries more, which is set at the zone.",
			},
			"de": {
				Label:       "Punkte pro Treffer",
				Description: "Was ein Treffer zählt, wenn die Zone nichts eigenes sagt.",
				Why:         "Jede Zone kann einen eigenen Wert tragen; dies ist der Wert für alle anderen. Eine Kopfzone trägt meist mehr, das wird an der Zone gesetzt.",
			},
		},
	},
	{
		Key: "rules.miss_penalty", Group: GroupRules, Control: ControlNumber, Min: n(0),
		Text: map[string]Texts{
			"en": {
				Label:       "Miss penalty",
				Description: "Points taken away for a shot that hits no live zone.",
				Why:         "It makes wild shooting cost something. Zero lets people shoot freely, which suits a first scenario for beginners.",
			},
			"de": {
				Label:       "Abzug bei Fehlschuss",
				Description: "Punkte, die ein Schuss ohne getroffene Zone kostet.",
				Why:         "So kostet wildes Schießen etwas. Null lässt frei schießen, was zu einem ersten Szenario für Anfänger passt.",
			},
		},
	},
	{
		Key: "rules.timeout_counts_as_hit", Group: GroupRules, Control: ControlSwitch,
		Text: map[string]Texts{
			"en": {
				Label:       "A timeout counts against the player",
				Description: "Whether a target that was not hit in time costs points and a life.",
				Why:         "With this on, letting a target go costs the penalty below, and a life when lives are in use. With it off, a timeout costs nothing anywhere in the scenario, whatever an appearance says.",
			},
			"de": {
				Label:       "Zeitablauf zählt gegen die Spielerin",
				Description: "Ob ein nicht rechtzeitig getroffenes Ziel Punkte und ein Leben kostet.",
				Why:         "Eingeschaltet kostet ein durchgelassenes Ziel den Abzug darunter, und ein Leben, wenn Leben im Spiel sind. Ausgeschaltet kostet Zeitablauf im ganzen Szenario nichts, ganz gleich was ein Auftritt sagt.",
			},
		},
	},
	{
		Key: "rules.timeout_penalty", Group: GroupRules, Control: ControlNumber, Min: n(0),
		Text: map[string]Texts{
			"en": {
				Label:       "Timeout penalty",
				Description: "Points taken away when a target times out.",
				Why:         "It only applies while the switch above is on and the appearance does not say nothing. Set it higher than the miss penalty when letting a target go should hurt more than a wild shot.",
			},
			"de": {
				Label:       "Abzug bei Zeitablauf",
				Description: "Punkte, die ein abgelaufenes Ziel kostet.",
				Why:         "Der Abzug gilt nur, solange der Schalter darüber an ist und der Auftritt nicht nichts sagt. Setzen Sie ihn höher als den Fehlschussabzug, wenn ein durchgelassenes Ziel mehr wehtun soll als ein wilder Schuss.",
			},
		},
	},
	{
		Key: "rules.lives", Group: GroupRules, Control: ControlNumber, Min: n(0),
		Text: map[string]Texts{
			"en": {
				Label:       "Lives",
				Description: "How many lives a player has; zero means lives are not in use.",
				Why:         "With lives the scenario ends when the last one is gone, which makes it a test of nerve. Without them it always runs to the end, which is friendlier for a first try.",
			},
			"de": {
				Label:       "Leben",
				Description: "Wie viele Leben eine Spielerin hat; null heißt, Leben sind nicht im Spiel.",
				Why:         "Mit Leben endet das Szenario, wenn das letzte weg ist, das macht es zur Nervenprobe. Ohne Leben läuft es immer bis zum Ende, was für einen ersten Versuch freundlicher ist.",
			},
		},
	},
	{
		Key: "rules.score_cap", Group: GroupRules, Control: ControlNumber, Min: n(0),
		Text: map[string]Texts{
			"en": {
				Label:       "Score cap",
				Description: "The highest score this scenario gives; zero means no cap.",
				Why:         "A cap keeps one scenario from deciding a whole ranking. Leave it at zero unless you run scenarios of very different lengths against each other.",
			},
			"de": {
				Label:       "Punktedeckel",
				Description: "Die höchste Punktzahl, die dieses Szenario vergibt; null heißt kein Deckel.",
				Why:         "Ein Deckel verhindert, dass ein Szenario eine ganze Rangliste entscheidet. Lassen Sie ihn auf null, außer Sie stellen sehr unterschiedlich lange Szenarien gegeneinander.",
			},
		},
	},

	// A zone on the canvas.
	{
		Key: "zone[#].id", Group: GroupZone, Control: ControlText,
		Text: map[string]Texts{
			"en": {
				Label:       "Zone id",
				Description: "The short name of the zone inside the scenario.",
				Why:         "Appearances and follow up reactions point at zones by this name. Lower case letters, digits, hyphens and underscores; z-head and z-torso read well later.",
			},
			"de": {
				Label:       "Zonen-Kennung",
				Description: "Der kurze Name der Zone innerhalb des Szenarios.",
				Why:         "Auftritte und Folgereaktionen verweisen über diesen Namen auf Zonen. Kleinbuchstaben, Ziffern, Bindestriche und Unterstriche; z-head und z-torso liest man später gut.",
			},
		},
	},
	{
		Key: "zone[#].name", Group: GroupZone, Control: ControlTexts, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Zone name",
				Description: "What this zone is called for a person, in every language.",
				Why:         "It appears in the editor and later in coaching, where a person reads which zone was hit. It may stay empty, then only the id is shown.",
			},
			"de": {
				Label:       "Zonenname",
				Description: "Wie diese Zone für Menschen heißt, in jeder Sprache.",
				Why:         "Er erscheint im Editor und später im Coaching, wo eine Person liest, welche Zone getroffen wurde. Er darf leer bleiben, dann steht nur die Kennung.",
			},
		},
	},
	{
		Key: "zone[#].shape", Group: GroupZone, Control: ControlFixed, Enum: scenario.Shapes(),
		Text: map[string]Texts{
			"en": {
				Label:       "Shape",
				Description: "Rectangle, circle or polygon; the drawing sets it.",
				Why:         "You choose the shape when you draw the zone. A rectangle is quickest, a circle fits a head, a polygon follows a body. To change the shape, delete the zone and draw it again.",
			},
			"de": {
				Label:       "Form",
				Description: "Rechteck, Kreis oder Vieleck; das Zeichnen setzt die Form.",
				Why:         "Sie wählen die Form beim Zeichnen der Zone. Ein Rechteck geht am schnellsten, ein Kreis passt auf einen Kopf, ein Vieleck folgt einem Körper. Für eine andere Form löschen Sie die Zone und zeichnen sie neu.",
			},
		},
	},
	{
		Key: "zone[#].points_value", Group: GroupZone, Control: ControlNumber, Min: n(0), Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Points for this zone",
				Description: "What a hit in this zone is worth; empty takes the value from the rules.",
				Why:         "This is where a head shot becomes worth more than a shot in the torso. Leave it empty for every zone that should simply count the usual value.",
			},
			"de": {
				Label:       "Punkte für diese Zone",
				Description: "Was ein Treffer in dieser Zone zählt; leer nimmt den Wert aus den Regeln.",
				Why:         "Hier wird ein Kopftreffer mehr wert als ein Treffer in den Rumpf. Lassen Sie das Feld leer für jede Zone, die einfach den üblichen Wert zählen soll.",
			},
		},
	},
	{
		Key: "zone[#].zone_class", Group: GroupZone, Control: ControlSelect, Enum: scenario.ZoneClasses(),
		Text: map[string]Texts{
			"en": {
				Label:       "Zone class",
				Description: "What part this zone is: head, torso, arm, leg, object or none.",
				Why:         "The class decides which follow up plays: a head shot can start another clip than a torso shot. It is also what coaching counts later.",
			},
			"de": {
				Label:       "Zonenart",
				Description: "Welcher Teil diese Zone ist: Kopf, Rumpf, Arm, Bein, Objekt oder keine.",
				Why:         "Die Art entscheidet, welche Folgereaktion spielt: Ein Kopftreffer kann einen anderen Clip starten als ein Rumpftreffer. Sie ist auch das, was das Coaching später zählt.",
			},
		},
	},

	// An appearance on the timeline.
	{
		Key: "appearance[#].id", Group: GroupAppearance, Control: ControlText,
		Text: map[string]Texts{
			"en": {
				Label:       "Appearance id",
				Description: "The short name of this appearance inside the scenario.",
				Why:         "Follow up reactions point at appearances by this name, and every hit in the journal carries it. a-zombie-1 reads well in a journal.",
			},
			"de": {
				Label:       "Auftritts-Kennung",
				Description: "Der kurze Name dieses Auftritts innerhalb des Szenarios.",
				Why:         "Folgereaktionen verweisen über diesen Namen auf Auftritte, und jeder Treffer im Journal trägt ihn. a-zombie-1 liest sich im Journal gut.",
			},
		},
	},
	{
		Key: "appearance[#].t_start_ms", Group: GroupAppearance, Control: ControlNumber, Unit: "ms", Min: n(0),
		Text: map[string]Texts{
			"en": {
				Label:       "Start",
				Description: "When the target shows up, in milliseconds on the scenario clock.",
				Why:         "From this moment the zones of the appearance can be hit. Drag the bar on the timeline instead of typing, and set the exact number here.",
			},
			"de": {
				Label:       "Beginn",
				Description: "Wann das Ziel auftaucht, in Millisekunden auf der Szenario-Uhr.",
				Why:         "Ab diesem Moment können die Zonen des Auftritts getroffen werden. Ziehen Sie den Balken auf der Zeitleiste statt zu tippen, und setzen Sie die genaue Zahl hier.",
			},
		},
	},
	{
		Key: "appearance[#].t_end_ms", Group: GroupAppearance, Control: ControlNumber, Unit: "ms", Min: n(1),
		Text: map[string]Texts{
			"en": {
				Label:       "End",
				Description: "When the target is gone again, in milliseconds.",
				Why:         "A target that was not hit often enough by this moment times out. The window must lie inside the duration of the scenario and end after it starts.",
			},
			"de": {
				Label:       "Ende",
				Description: "Wann das Ziel wieder weg ist, in Millisekunden.",
				Why:         "Ein Ziel, das bis dahin nicht oft genug getroffen wurde, läuft ab. Das Fenster muss in der Dauer des Szenarios liegen und nach seinem Beginn enden.",
			},
		},
	},
	{
		Key: "appearance[#].zones", Group: GroupAppearance, Control: ControlZones,
		Text: map[string]Texts{
			"en": {
				Label:       "Zones",
				Description: "The zones that are live while this target is up.",
				Why:         "Every zone belongs to exactly one appearance: a head and a torso of the same figure belong together. A zone without an appearance is refused at validation.",
			},
			"de": {
				Label:       "Zonen",
				Description: "Die Zonen, die scharf sind, solange dieses Ziel steht.",
				Why:         "Jede Zone gehört zu genau einem Auftritt: Kopf und Rumpf derselben Figur gehören zusammen. Eine Zone ohne Auftritt wird bei der Prüfung abgewiesen.",
			},
		},
	},
	{
		Key: "appearance[#].required_hits", Group: GroupAppearance, Control: ControlNumber, Min: n(1),
		Text: map[string]Texts{
			"en": {
				Label:       "Hits needed",
				Description: "How many hits clear this target.",
				Why:         "One hit is the usual case. Two or more make a target that takes work, and the follow up only plays when the last one lands.",
			},
			"de": {
				Label:       "Nötige Treffer",
				Description: "Wie viele Treffer dieses Ziel erledigen.",
				Why:         "Ein Treffer ist der Normalfall. Zwei oder mehr machen ein Ziel, das Arbeit kostet, und die Folgereaktion spielt erst beim letzten.",
			},
		},
	},
	{
		Key: "appearance[#].on_timeout", Group: GroupAppearance, Control: ControlSelect, Enum: scenario.OnTimeouts(),
		Text: map[string]Texts{
			"en": {
				Label:       "When time runs out",
				Description: "Penalty, nothing, or the scenario ends.",
				Why:         "Penalty costs what the rules say, as long as the timeout switch there is on. Nothing lets the target go without cost. End stops the scenario, which suits a target that must not be missed.",
			},
			"de": {
				Label:       "Wenn die Zeit abläuft",
				Description: "Abzug, nichts, oder das Szenario endet.",
				Why:         "Abzug kostet, was die Regeln sagen, solange der Zeitablauf-Schalter dort an ist. Nichts lässt das Ziel ohne Kosten ziehen. Ende beendet das Szenario, was zu einem Ziel passt, das man nicht verfehlen darf.",
			},
		},
	},
	{
		Key: "appearance[#].media_state", Group: GroupAppearance, Control: ControlSelect, From: FromStates, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Media state",
				Description: "Which clip plays while this target is up.",
				Why:         "Interactive scenarios carry one clip per state: walking, dying from a head shot, dying from a body shot. This is the state that runs while the target stands.",
			},
			"de": {
				Label:       "Medienzustand",
				Description: "Welcher Clip spielt, solange dieses Ziel steht.",
				Why:         "Interaktive Szenarien tragen einen Clip je Zustand: gehen, am Kopftreffer sterben, am Rumpftreffer sterben. Dies ist der Zustand, der läuft, solange das Ziel steht.",
			},
		},
	},

	// The immediate reaction, which every tier has.
	{
		Key: "reaction.immediate.flash", Group: GroupImmediate, Control: ControlSwitch,
		Text: map[string]Texts{
			"en": {
				Label:       "Flash",
				Description: "A short bright flash in the frame of the hit.",
				Why:         "It tells the shooter that the shot landed, before any clip can switch. It costs nothing and works on every target.",
			},
			"de": {
				Label:       "Blitz",
				Description: "Ein kurzer heller Blitz im Bild des Treffers.",
				Why:         "Er sagt der Schützin, dass der Schuss saß, bevor irgendein Clip wechseln kann. Er kostet nichts und funktioniert auf jedem Ziel.",
			},
		},
	},
	{
		Key: "reaction.immediate.impact_sound", Group: GroupImmediate, Control: ControlSelect, From: FromSounds, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Impact sound",
				Description: "The sound of a hit, an Ogg file from the media panel.",
				Why:         "Sound carries further than light in a loud room. Keep it short; it plays in the same frame as the hit.",
			},
			"de": {
				Label:       "Trefferton",
				Description: "Der Klang eines Treffers, eine Ogg-Datei aus dem Medienbereich.",
				Why:         "Klang trägt in einem lauten Raum weiter als Licht. Halten Sie ihn kurz; er spielt im selben Bild wie der Treffer.",
			},
		},
	},
	{
		Key: "reaction.immediate.hitmarker", Group: GroupImmediate, Control: ControlSelect, Enum: scenario.Hitmarkers(),
		Text: map[string]Texts{
			"en": {
				Label:       "Hit marker",
				Description: "The mark drawn where the shot landed: ring, cross or none.",
				Why:         "It shows where the shot went, which is what a shooter learns from. None suits a scenario that should look like a film.",
			},
			"de": {
				Label:       "Treffermarke",
				Description: "Die Marke am Einschlag: Ring, Kreuz oder keine.",
				Why:         "Sie zeigt, wohin der Schuss ging, und genau daraus lernt eine Schützin. Keine passt zu einem Szenario, das wie ein Film aussehen soll.",
			},
		},
	},
	{
		Key: "reaction.immediate.blood", Group: GroupImmediate, Control: ControlSelect, From: FromOverlays, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Overlay clip",
				Description: "A short clip with transparency drawn over the hit, a WebM file.",
				Why:         "It is the blood or the spark of the hit. It needs transparency, which is why it is WebM; leave it empty for a scenario without it.",
			},
			"de": {
				Label:       "Overlay-Clip",
				Description: "Ein kurzer Clip mit Transparenz über dem Treffer, eine WebM-Datei.",
				Why:         "Er ist das Blut oder der Funke des Treffers. Er braucht Transparenz, deshalb WebM; lassen Sie ihn leer für ein Szenario ohne.",
			},
		},
	},

	// A follow up reaction of a character.
	{
		Key: "reaction.followup[#].appearance", Group: GroupFollowup, Control: ControlSelect, From: FromAppearances,
		Text: map[string]Texts{
			"en": {
				Label:       "Appearance",
				Description: "The target this follow up belongs to.",
				Why:         "A follow up is how one character reacts. Each appearance may have one follow up per zone class, no more.",
			},
			"de": {
				Label:       "Auftritt",
				Description: "Das Ziel, zu dem diese Folgereaktion gehört.",
				Why:         "Eine Folgereaktion ist die Reaktion einer Figur. Je Auftritt gibt es höchstens eine Folgereaktion pro Zonenart.",
			},
		},
	},
	{
		Key: "reaction.followup[#].zone_class", Group: GroupFollowup, Control: ControlSelect, Enum: scenario.ZoneClasses(),
		Text: map[string]Texts{
			"en": {
				Label:       "Zone class",
				Description: "Which kind of hit starts this follow up.",
				Why:         "A head shot may drop the figure at once while a torso shot only makes it stagger. That difference is made here.",
			},
			"de": {
				Label:       "Zonenart",
				Description: "Welche Art Treffer diese Folgereaktion auslöst.",
				Why:         "Ein Kopftreffer darf die Figur sofort fallen lassen, während ein Rumpftreffer sie nur taumeln lässt. Dieser Unterschied wird hier gemacht.",
			},
		},
	},
	{
		Key: "reaction.followup[#].media_state", Group: GroupFollowup, Control: ControlSelect, From: FromStates,
		Text: map[string]Texts{
			"en": {
				Label:       "Clip",
				Description: "The media state that plays as the reaction.",
				Why:         "It is the dying, the stagger or the flight. Upload the clip in the media panel and give it a state name there first.",
			},
			"de": {
				Label:       "Clip",
				Description: "Der Medienzustand, der als Reaktion spielt.",
				Why:         "Er ist das Sterben, das Taumeln oder die Flucht. Laden Sie den Clip im Medienbereich hoch und geben Sie ihm dort zuerst einen Zustandsnamen.",
			},
		},
	},
	{
		Key: "reaction.followup[#].duration_ms", Group: GroupFollowup, Control: ControlNumber, Unit: "ms", Min: n(1),
		Text: map[string]Texts{
			"en": {
				Label:       "Duration",
				Description: "How long the reaction plays, in milliseconds.",
				Why:         "Usually the length of the clip. Shorter cuts it off, longer holds its last picture.",
			},
			"de": {
				Label:       "Dauer",
				Description: "Wie lange die Reaktion spielt, in Millisekunden.",
				Why:         "Üblicherweise die Länge des Clips. Kürzer schneidet ihn ab, länger hält sein letztes Bild.",
			},
		},
	},
	{
		Key: "reaction.followup[#].then", Group: GroupFollowup, Control: ControlSelect, From: FromThen,
		Text: map[string]Texts{
			"en": {
				Label:       "And then",
				Description: "What happens after the reaction: the figure is done, back to a state, or on to the next target.",
				Why:         "Done leaves the last picture standing until another target sets a state. Back to a state lets a figure that only staggered walk again. On to the next target moves the clock forward to the next appearance.",
			},
			"de": {
				Label:       "Und dann",
				Description: "Was nach der Reaktion geschieht: Die Figur ist fertig, zurück in einen Zustand, oder weiter zum nächsten Ziel.",
				Why:         "Fertig lässt das letzte Bild stehen, bis ein anderes Ziel einen Zustand setzt. Zurück in einen Zustand lässt eine nur taumelnde Figur weitergehen. Weiter zum nächsten Ziel zieht die Uhr zum nächsten Auftritt vor.",
			},
		},
	},

	// The media of the scenario.
	{
		Key: "media.main", Group: GroupMedia, Control: ControlSelect, From: FromClips,
		Text: map[string]Texts{
			"en": {
				Label:       "Main video",
				Description: "The one video of a video scenario.",
				Why:         "It runs from the first to the last second, and the timeline is its time. Upload it first; the editor takes the duration and the canvas from it.",
			},
			"de": {
				Label:       "Hauptvideo",
				Description: "Das eine Video eines Video-Szenarios.",
				Why:         "Es läuft von der ersten bis zur letzten Sekunde, und die Zeitleiste ist seine Zeit. Laden Sie es zuerst hoch; der Editor übernimmt Dauer und Fläche daraus.",
			},
		},
	},
	{
		Key: "media.state", Group: GroupMedia, Control: ControlStates,
		Text: map[string]Texts{
			"en": {
				Label:       "Media states",
				Description: "The named clips of an interactive scenario and the file of each.",
				Why:         "Every state is one clip: walk, die-head, die-body. Appearances and follow ups name these states, so give them names you still understand in a year.",
			},
			"de": {
				Label:       "Medienzustände",
				Description: "Die benannten Clips eines interaktiven Szenarios und die Datei zu jedem.",
				Why:         "Jeder Zustand ist ein Clip: walk, die-head, die-body. Auftritte und Folgereaktionen nennen diese Zustände, geben Sie ihnen also Namen, die Sie in einem Jahr noch verstehen.",
			},
		},
	},
}
