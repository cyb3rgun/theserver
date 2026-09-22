// Package venue holds the fields of a site and of a room (D-066): every
// field an operator edits, how the page shows it, and its label, its short
// description and the longer why in every language. The settings registry,
// the editor registry and the target type registry have the same shape, and
// the admin pages render them all with one help component.
package venue

import (
	"slices"
	"strings"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The controls a field is shown with.
const (
	ControlText   = "text"
	ControlTexts  = "texts"
	ControlNumber = "number"
	ControlSelect = "select"
	// ControlMoney is a whole number in the smallest unit of a currency.
	ControlMoney = "money"
	// ControlDate is a day, written as a date and stored as unix
	// milliseconds.
	ControlDate = "date"
)

// A Field is one field of a site or of a room.
type Field struct {
	Key      string
	Control  string
	Enum     []string
	Unit     string
	Min      *float64
	Max      *float64
	Decimals bool
	Optional bool
	// Licence marks a field the manufacturer sets, not the operator
	// (D-070).
	Licence bool
	Text    map[string]Texts
}

// Texts are the three texts of a field in one language.
type Texts struct {
	Label       string
	Description string
	Why         string
}

func n(v float64) *float64 { return &v }

// SiteFields lists the fields of a site, in the order the form shows them.
func SiteFields() []Field { return slices.Clone(siteRegistry) }

// RoomFields lists the fields of a room, in the order the form shows them.
func RoomFields() []Field { return slices.Clone(roomRegistry) }

// In returns the texts of a field in lang, falling back to English.
func (f Field) In(lang string) Texts {
	if t, ok := f.Text[lang]; ok && t.Label != "" {
		return t
	}
	return f.Text["en"]
}

// ID is the key in a form that an html id takes.
func (f Field) ID() string {
	return "vn-" + strings.ReplaceAll(f.Key, "_", "-")
}

var siteRegistry = []Field{
	{
		Key: "id", Control: ControlText,
		Text: map[string]Texts{
			"en": {
				Label:       "Site id",
				Description: "The short name the server keeps the site under, set once.",
				Why:         "Rooms, devices and later the statement name the site by this id. It holds lower case letters, digits, hyphens and underscores and cannot be changed afterwards.",
			},
			"de": {
				Label:       "Standort-Kennung",
				Description: "Der kurze Name, unter dem der Server den Standort führt, einmal vergeben.",
				Why:         "Räume, Geräte und später die Abrechnung nennen den Standort über diese Kennung. Sie enthält kleine Buchstaben, Ziffern, Bindestriche und Unterstriche und lässt sich später nicht ändern.",
			},
		},
	},
	{
		Key: "name", Control: ControlText,
		Text: map[string]Texts{
			"en": {
				Label:       "Name",
				Description: "The name of the venue as people say it.",
				Why:         "It stands on every page that names the site, and later on the statement. A venue that is renamed keeps its id.",
			},
			"de": {
				Label:       "Name",
				Description: "Der Name des Standorts, wie Menschen ihn sagen.",
				Why:         "Er steht auf jeder Seite, die den Standort nennt, und später auf der Abrechnung. Ein umbenannter Standort behält seine Kennung.",
			},
		},
	},
	{
		Key: "address", Control: ControlText, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Address",
				Description: "Street, number, postcode and town.",
				Why:         "The address is what a player finds the venue by, and what the map of the federation will show one day. It is one line; a second one is a note.",
			},
			"de": {
				Label:       "Adresse",
				Description: "Straße, Hausnummer, Postleitzahl und Ort.",
				Why:         "Über die Adresse findet ein Spieler den Standort, und eines Tages zeigt die Karte der Föderation sie. Sie ist eine Zeile; eine zweite gehört in die Notizen.",
			},
		},
	},
	{
		Key: "timezone", Control: ControlText, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Timezone",
				Description: "The IANA name of the timezone, such as Europe/Berlin.",
				Why:         "Sessions are stored in unix time, which knows no timezone. The name says how a day of this venue is counted, which the reports of a month will need.",
			},
			"de": {
				Label:       "Zeitzone",
				Description: "Der IANA-Name der Zeitzone, etwa Europe/Berlin.",
				Why:         "Sitzungen werden in Unixzeit gespeichert, die keine Zeitzone kennt. Der Name sagt, wie ein Tag dieses Standorts gezählt wird, was die Monatsberichte brauchen werden.",
			},
		},
	},
	{
		Key: "contact", Control: ControlText, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Contact",
				Description: "Who is called when something at this venue stands still.",
				Why:         "A telephone number or an address of a person, not of a department. It is read by somebody who is already in a hurry.",
			},
			"de": {
				Label:       "Kontakt",
				Description: "Wen man anruft, wenn an diesem Standort etwas stillsteht.",
				Why:         "Eine Telefonnummer oder Adresse einer Person, nicht einer Abteilung. Sie wird von jemandem gelesen, der schon in Eile ist.",
			},
		},
	},
	{
		Key: "notes", Control: ControlTexts, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Notes",
				Description: "What the next person should know about this venue.",
				Why:         "Where the fuse box is, which door sticks, who holds the key. It may stay empty, but it is the only place where such a thing is written down.",
			},
			"de": {
				Label:       "Notizen",
				Description: "Was die nächste Person über diesen Standort wissen sollte.",
				Why:         "Wo der Sicherungskasten ist, welche Tür klemmt, wer den Schlüssel hat. Darf leer bleiben, ist aber die einzige Stelle, an der so etwas festgehalten wird.",
			},
		},
	},
	{
		Key: "licence_id", Control: ControlText, Optional: true, Licence: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Licence id",
				Description: "The number this venue is licensed under.",
				Why:         "It is set by the manufacturer and belongs to the franchise statement, which is not built yet. It is stored and shown so that the table does not have to be rebuilt when the money side arrives.",
			},
			"de": {
				Label:       "Lizenznummer",
				Description: "Die Nummer, unter der dieser Standort lizenziert ist.",
				Why:         "Sie wird vom Hersteller gesetzt und gehört zur Franchise-Abrechnung, die es noch nicht gibt. Sie wird gespeichert und gezeigt, damit die Tabelle nicht neu gebaut werden muss, wenn die Geldseite kommt.",
			},
		},
	},
	{
		Key: "franchise_rate", Control: ControlNumber, Unit: "%", Min: n(0), Max: n(100), Decimals: true, Licence: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Franchise rate",
				Description: "The share of the system revenue above the threshold, in percent.",
				Why:         "Set by the manufacturer, per venue, so that early partners can be treated differently from venue number fifty. Nothing computes with it yet.",
			},
			"de": {
				Label:       "Franchise-Satz",
				Description: "Der Anteil am Systemumsatz oberhalb der Schwelle, in Prozent.",
				Why:         "Vom Hersteller je Standort gesetzt, damit frühe Partner anders behandelt werden können als Standort Nummer fünfzig. Es rechnet noch nichts damit.",
			},
		},
	},
	{
		Key: "monthly_threshold", Control: ControlMoney, Min: n(0), Licence: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Monthly threshold",
				Description: "The monthly revenue below which nothing is owed, in the smallest unit of the currency.",
				Why:         "Money is kept in whole cents, never in a fraction, because a fraction of a cent is a rounding error waiting to be argued about. 500000 is five thousand euros.",
			},
			"de": {
				Label:       "Monatliche Schwelle",
				Description: "Der Monatsumsatz, unterhalb dessen nichts fällig wird, in der kleinsten Einheit der Währung.",
				Why:         "Geld wird in ganzen Cent geführt, nie in Bruchteilen, weil ein Bruchteil eines Cent ein Rundungsfehler ist, über den man später streitet. 500000 sind fünftausend Euro.",
			},
		},
	},
	{
		Key: "currency", Control: ControlText, Licence: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Currency",
				Description: "The three letter code of the currency, such as EUR.",
				Why:         "It says what the threshold counts in. A venue in another country keeps its own currency; nothing is converted.",
			},
			"de": {
				Label:       "Währung",
				Description: "Der dreibuchstabige Code der Währung, etwa EUR.",
				Why:         "Er sagt, worin die Schwelle zählt. Ein Standort in einem anderen Land behält seine eigene Währung; es wird nichts umgerechnet.",
			},
		},
	},
	{
		Key: "valid_from", Control: ControlDate, Optional: true, Licence: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Valid from",
				Description: "The day these licence values start to count.",
				Why:         "A rate that changes does not change the past. The day says from when the new values hold, so an older month can still be read the way it was agreed.",
			},
			"de": {
				Label:       "Gültig ab",
				Description: "Der Tag, ab dem diese Lizenzwerte gelten.",
				Why:         "Ein geänderter Satz ändert die Vergangenheit nicht. Der Tag sagt, ab wann die neuen Werte gelten, damit ein älterer Monat so gelesen werden kann, wie er vereinbart war.",
			},
		},
	},
}

var roomRegistry = []Field{
	{
		Key: "id", Control: ControlText,
		Text: map[string]Texts{
			"en": {
				Label:       "Room id",
				Description: "The short name the server keeps the room under, set once.",
				Why:         "Devices and sessions name the room by this id. It holds lower case letters, digits, hyphens and underscores and cannot be changed afterwards.",
			},
			"de": {
				Label:       "Raum-Kennung",
				Description: "Der kurze Name, unter dem der Server den Raum führt, einmal vergeben.",
				Why:         "Geräte und Sitzungen nennen den Raum über diese Kennung. Sie enthält kleine Buchstaben, Ziffern, Bindestriche und Unterstriche und lässt sich später nicht ändern.",
			},
		},
	},
	{
		Key: "name", Control: ControlText,
		Text: map[string]Texts{
			"en": {
				Label:       "Name",
				Description: "The name of the room as the people at the counter say it.",
				Why:         "It stands on the session, on the device page and on the sign at the door. Say it the way it is said in the house.",
			},
			"de": {
				Label:       "Name",
				Description: "Der Name des Raums, wie ihn die Leute am Tresen sagen.",
				Why:         "Er steht an der Sitzung, auf der Geräteseite und am Schild an der Tür. Nennen Sie ihn so, wie er im Haus genannt wird.",
			},
		},
	},
	{
		Key: "age_rating", Control: ControlSelect, Enum: scenario.AgeRatings(),
		Text: map[string]Texts{
			"en": {
				Label:       "Age rating",
				Description: "The age of the players who play in this room.",
				Why:         "A scenario rated above it cannot be given to a session of this room. The age a single device is set for stays beside it, so one target can be stricter than the room.",
			},
			"de": {
				Label:       "Altersfreigabe",
				Description: "Das Alter der Spieler, die in diesem Raum spielen.",
				Why:         "Ein höher eingestuftes Szenario kann einer Sitzung dieses Raums nicht gegeben werden. Das Alter eines einzelnen Geräts bleibt daneben bestehen, ein Ziel kann also strenger sein als der Raum.",
			},
		},
	},
	{
		Key: "wifi_channel", Control: ControlNumber, Min: n(0), Max: n(196),
		Text: map[string]Texts{
			"en": {
				Label:       "WiFi channel",
				Description: "The channel of the access point in this room, 0 while it is not set.",
				Why:         "Targets and the pistol share the air. Writing the channel down here is what makes two rooms that interfere findable, before somebody measures for an hour.",
			},
			"de": {
				Label:       "WLAN-Kanal",
				Description: "Der Kanal des Access Points in diesem Raum, 0 solange er nicht gesetzt ist.",
				Why:         "Ziele und Pistole teilen sich die Luft. Der hier notierte Kanal macht zwei Räume, die sich stören, auffindbar, bevor jemand eine Stunde misst.",
			},
		},
	},
	{
		Key: "beacon_period_ms", Control: ControlNumber, Unit: "ms", Min: n(20), Max: n(5000),
		Text: map[string]Texts{
			"en": {
				Label:       "Beacon period",
				Description: "How long one round of the beacon multiplex takes.",
				Why:         "Every target of the room sends in its own slot of this round, so that the pistol can tell them apart. A shorter round finds a target faster and leaves less air for the others.",
			},
			"de": {
				Label:       "Baken-Periode",
				Description: "Wie lange eine Runde des Baken-Multiplex dauert.",
				Why:         "Jedes Ziel des Raums sendet in seinem eigenen Slot dieser Runde, damit die Pistole sie auseinanderhalten kann. Eine kürzere Runde findet ein Ziel schneller und lässt den anderen weniger Luft.",
			},
		},
	},
	{
		Key: "beacon_slots", Control: ControlNumber, Min: n(1), Max: n(32),
		Text: map[string]Texts{
			"en": {
				Label:       "Slots in a round",
				Description: "How many slots one round has, which is how many targets the room can hold.",
				Why:         "A target takes exactly one slot, and two targets of a room can never hold the same one. Leave a slot or two free for the target that is hung next week.",
			},
			"de": {
				Label:       "Slots je Runde",
				Description: "Wie viele Slots eine Runde hat, also wie viele Ziele der Raum tragen kann.",
				Why:         "Ein Ziel belegt genau einen Slot, und zwei Ziele eines Raums können nie denselben halten. Lassen Sie ein bis zwei Slots frei für das Ziel, das nächste Woche hängt.",
			},
		},
	},
	{
		Key: "capacity", Control: ControlNumber, Min: n(0), Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Capacity",
				Description: "How many players fit into this room, 0 while it is not set.",
				Why:         "It is what the counter books against, once booking exists. Today it is a number on the page and nothing else.",
			},
			"de": {
				Label:       "Kapazität",
				Description: "Wie viele Spieler in diesen Raum passen, 0 solange es nicht gesetzt ist.",
				Why:         "Dagegen bucht der Tresen, sobald es Buchungen gibt. Heute ist es eine Zahl auf der Seite und sonst nichts.",
			},
		},
	},
	{
		Key: "notes", Control: ControlTexts, Optional: true,
		Text: map[string]Texts{
			"en": {
				Label:       "Notes",
				Description: "What the next person should know about this room.",
				Why:         "Which lane is dark, where the cable runs, what the noise at the back is. It may stay empty.",
			},
			"de": {
				Label:       "Notizen",
				Description: "Was die nächste Person über diesen Raum wissen sollte.",
				Why:         "Welche Bahn dunkel ist, wo das Kabel läuft, was das Geräusch hinten ist. Darf leer bleiben.",
			},
		},
	},
}
