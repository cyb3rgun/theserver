// Package scenario is the one implementation of docs/scenario.md, version 1
// (D-035): the manifest of a scenario package, reading a package from a
// directory or a zip, validation with typed problems that a page can
// translate, and the manifest hash.
//
// theserver validates with it on upload, theclient on install, and the
// editor before export, so the package depends on nothing of the server: not
// on the store, not on the API.
package scenario

import (
	"fmt"
	"slices"
)

// ModelVersion is the version of docs/scenario.md this package implements.
const ModelVersion = 1

// The names a package gives its files (scenario.md section 2). The manifest
// and the signature are not listed in the files table; every other file is.
const (
	ManifestName  = "manifest.toml"
	SignatureName = "SIGNATURE"
	CoverName     = "cover.png"
)

// The tiers of a scenario (scenario.md section 1).
const (
	TierVideo       = "video"
	TierInteractive = "interactive"
	TierLayered     = "layered"
	TierRealtime    = "realtime"
)

// Tiers lists the tiers in the order of the document.
func Tiers() []string {
	return []string{TierVideo, TierInteractive, TierLayered, TierRealtime}
}

// AgeRatings lists the values age_rating may take, youngest first.
func AgeRatings() []string {
	return []string{"0", "6", "12", "16", "18"}
}

// TextLanguages lists the languages every text of a manifest needs.
func TextLanguages() []string {
	return []string{"en", "de"}
}

// The allowed values of the fields with a fixed set (scenario.md sections 2
// to 5). Orientation and fit name only one value in the document; the sets
// here are recorded in D-035.
var (
	licences    = []string{"official", "community", "private"}
	orientation = []string{"portrait", "landscape"}
	fits        = []string{"cover", "contain", "fill"}
	shapes      = []string{"rect", "circle", "polygon"}
	zoneClasses = []string{"head", "torso", "arm", "leg", "object", "none"}
	onTimeouts  = []string{"penalty", "nothing", "end"}
	hitmarkers  = []string{"ring", "cross", "none"}
)

// ZoneClasses lists the classes of a zone.
func ZoneClasses() []string {
	return slices.Clone(zoneClasses)
}

// Licences lists the licences a scenario may carry.
func Licences() []string {
	return slices.Clone(licences)
}

// Orientations lists how a target may be mounted.
func Orientations() []string {
	return slices.Clone(orientation)
}

// Fits lists how a picture fills a screen of another shape.
func Fits() []string {
	return slices.Clone(fits)
}

// Shapes lists the shapes a zone may have.
func Shapes() []string {
	return slices.Clone(shapes)
}

// OnTimeouts lists what an appearance does when its time runs out.
func OnTimeouts() []string {
	return slices.Clone(onTimeouts)
}

// Hitmarkers lists the marks an immediate reaction may draw.
func Hitmarkers() []string {
	return slices.Clone(hitmarkers)
}

// Manifest is manifest.toml. The toml and json names are the same; Hash
// encodes the json form.
type Manifest struct {
	Scenario    Info              `toml:"scenario" json:"scenario"`
	Display     *Display          `toml:"display" json:"display"`
	Rules       *Rules            `toml:"rules" json:"rules"`
	Zones       []Zone            `toml:"zone" json:"zone"`
	Appearances []Appearance      `toml:"appearance" json:"appearance"`
	Reaction    Reaction          `toml:"reaction" json:"reaction"`
	Media       Media             `toml:"media" json:"media"`
	Files       map[string]string `toml:"files" json:"files"`

	// unknown lists the keys of the TOML file that the model does not have.
	unknown []string
}

// Info is the [scenario] section.
type Info struct {
	// ID is stable, lower case, without spaces; the content store and the
	// download address use it.
	ID string `toml:"id" json:"id"`
	// Version counts the published changes, from 1 (D-037).
	Version     int    `toml:"version" json:"version"`
	Tier        string `toml:"tier" json:"tier"`
	Title       Text   `toml:"title" json:"title"`
	Description Text   `toml:"description" json:"description"`
	// AgeRating is one of AgeRatings; theserver enforces it (D-039).
	AgeRating string `toml:"age_rating" json:"age_rating"`
	DurationS int    `toml:"duration_s" json:"duration_s"`
	Author    string `toml:"author" json:"author"`
	Licence   string `toml:"licence" json:"licence"`
}

// Text is a text in several languages, keyed by language.
type Text map[string]string

// In returns the text in lang, else in English, else empty.
func (t Text) In(lang string) string {
	if text, ok := t[lang]; ok && text != "" {
		return text
	}
	return t["en"]
}

// Display is the [display] section. All zones are in canvas coordinates,
// origin top left. TargetType is optional and names the kind of target the
// scenario was made for (D-062); its canvas is the resolution of that type.
// A scenario without one keeps its own canvas and plays on any target.
type Display struct {
	Canvas      *Canvas `toml:"canvas" json:"canvas"`
	Orientation string  `toml:"orientation" json:"orientation"`
	Fit         string  `toml:"fit" json:"fit"`
	TargetType  string  `toml:"target_type" json:"target_type,omitempty"`
}

// Canvas is the coordinate space of the zones.
type Canvas struct {
	W int `toml:"w" json:"w"`
	H int `toml:"h" json:"h"`
}

// Rules is the [rules] section. A score cap of 0 means none; lives of 0 means
// lives are not in use.
type Rules struct {
	PointsPerHitDefault int  `toml:"points_per_hit_default" json:"points_per_hit_default"`
	MissPenalty         int  `toml:"miss_penalty" json:"miss_penalty"`
	TimeoutCountsAsHit  bool `toml:"timeout_counts_as_hit" json:"timeout_counts_as_hit"`
	TimeoutPenalty      int  `toml:"timeout_penalty" json:"timeout_penalty"`
	Lives               int  `toml:"lives" json:"lives"`
	ScoreCap            int  `toml:"score_cap" json:"score_cap"`
}

// Zone is one [[zone]]: an area on the canvas that can be hit. Every shape is
// written as points (D-035): a polygon has at least three, a rect two
// opposite corners, a circle its centre and a Radius.
type Zone struct {
	ID     string  `toml:"id" json:"id"`
	Name   Text    `toml:"name" json:"name"`
	Shape  string  `toml:"shape" json:"shape"`
	Points [][]int `toml:"points" json:"points"`
	Radius int     `toml:"radius" json:"radius"`
	// PointsValue is what a hit is worth; nil means the default of the
	// rules.
	PointsValue *int   `toml:"points_value" json:"points_value"`
	ZoneClass   string `toml:"zone_class" json:"zone_class"`
	// Keyframes move the zone in time (INTERACTIVE, LAYERED); each carries
	// the shape fields of the zone.
	Keyframes []Keyframe `toml:"keyframe" json:"keyframe"`
}

// Keyframe is one [[zone.keyframe]]: the shape of its zone at TMs on the
// scenario clock.
type Keyframe struct {
	TMs    int     `toml:"t_ms" json:"t_ms"`
	Points [][]int `toml:"points" json:"points"`
	Radius int     `toml:"radius" json:"radius"`
}

// Appearance is one [[appearance]]: a target showing up between TStartMs
// and TEndMs with its zones live.
type Appearance struct {
	ID           string   `toml:"id" json:"id"`
	TStartMs     int      `toml:"t_start_ms" json:"t_start_ms"`
	TEndMs       int      `toml:"t_end_ms" json:"t_end_ms"`
	Zones        []string `toml:"zones" json:"zones"`
	RequiredHits int      `toml:"required_hits" json:"required_hits"`
	OnTimeout    string   `toml:"on_timeout" json:"on_timeout"`
	// MediaState is the state that plays while the appearance is up
	// (INTERACTIVE, LAYERED).
	MediaState string `toml:"media_state" json:"media_state"`
	// Layer is the character layer MediaState belongs to (LAYERED only,
	// S01-B08). The json name leaves an empty layer out, so every manifest
	// written before the field keeps its canonical form and its hash.
	Layer string `toml:"layer" json:"layer,omitempty"`
}

// Reaction is the [reaction] section.
type Reaction struct {
	Immediate *Immediate `toml:"immediate" json:"immediate"`
	Followups []Followup `toml:"followup" json:"followup"`
}

// Immediate fires in the frame of the hit, from the target's own assets.
type Immediate struct {
	Flash       bool   `toml:"flash" json:"flash"`
	ImpactSound string `toml:"impact_sound" json:"impact_sound"`
	Hitmarker   string `toml:"hitmarker" json:"hitmarker"`
	Blood       string `toml:"blood" json:"blood"`
}

// Followup is one [[reaction.followup]]: how the character of an appearance
// reacts to a hit in a zone class. VIDEO has none; for REALTIME MediaState
// names an event of the renderer.
type Followup struct {
	Appearance string `toml:"appearance" json:"appearance"`
	ZoneClass  string `toml:"zone_class" json:"zone_class"`
	MediaState string `toml:"media_state" json:"media_state"`
	DurationMs int    `toml:"duration_ms" json:"duration_ms"`
	// Then is end, next, or back:<state>.
	Then string `toml:"then" json:"then"`
}

// Media is the [media] section; each tier has its own part (scenario.md
// section 6). Paths are relative to the package, with forward slashes.
type Media struct {
	// Main is the one video of VIDEO.
	Main string `toml:"main" json:"main"`
	// State maps the states of INTERACTIVE to their clips, written as the
	// table [media.state] (D-035).
	State map[string]string `toml:"state" json:"state"`
	// Background and Layer are LAYERED.
	Background *Background      `toml:"background" json:"background"`
	Layer      map[string]Layer `toml:"layer" json:"layer"`
	// Realtime is REALTIME.
	Realtime *Realtime `toml:"realtime" json:"realtime"`
}

// Background is the clip behind the layers of LAYERED.
type Background struct {
	Clip string `toml:"clip" json:"clip"`
}

// Layer is one character layer of LAYERED with its states.
type Layer struct {
	States map[string]string `toml:"states" json:"states"`
	Z      int               `toml:"z" json:"z"`
}

// Realtime names what thegame renders.
type Realtime struct {
	World      string   `toml:"world" json:"world"`
	Characters []string `toml:"characters" json:"characters"`
}

// A Problem is one reason a package is not valid, in a form a page can
// translate: Code has a text in every language of the catalogues (D-040),
// Field names the place in the manifest, Detail says it in English.
type Problem struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (p Problem) String() string {
	return fmt.Sprintf("%s: %s: %s", p.Field, p.Code, p.Detail)
}

// The codes of a Problem. Load and LoadZip report the first three as a
// *LoadError; Validate reports the others; the server reports
// CodeVersionTaken at upload.
const (
	CodeBadPackage             = "bad_package"
	CodeNoManifest             = "no_manifest"
	CodeBadManifest            = "bad_manifest"
	CodeUnknownField           = "unknown_field"
	CodeMissingSection         = "missing_section"
	CodeBadID                  = "bad_id"
	CodeDuplicateID            = "duplicate_id"
	CodeBadVersion             = "bad_version"
	CodeBadTier                = "bad_tier"
	CodeMissingText            = "missing_text"
	CodeNoAgeRating            = "no_age_rating"
	CodeBadAgeRating           = "bad_age_rating"
	CodeBadValue               = "bad_value"
	CodeNoCanvas               = "no_canvas"
	CodeBadShape               = "bad_shape"
	CodeKeyframesUnordered     = "keyframes_unordered"
	CodeBadTimeWindow          = "bad_time_window"
	CodeZoneWithoutAppearance  = "zone_without_appearance"
	CodeZoneShared             = "zone_shared"
	CodeAppearanceUnknownZone  = "appearance_unknown_zone"
	CodeAppearanceWithoutZones = "appearance_without_zones"
	CodeUnknownAppearance      = "unknown_appearance"
	CodeUnknownMediaState      = "unknown_media_state"
	CodeNotInTier              = "not_in_tier"
	CodeTierMediaMismatch      = "tier_media_mismatch"
	CodeMissingMedia           = "missing_media"
	CodeHashMismatch           = "hash_mismatch"
	CodeUnlistedFile           = "unlisted_file"
	CodeVersionTaken           = "version_taken"
	// CodeUnknownTargetType is a [display] section that names a target type
	// the server does not have (D-062).
	CodeUnknownTargetType = "unknown_target_type"
)

// Codes lists every code, in the order of the constants.
func Codes() []string {
	return []string{
		CodeBadPackage, CodeNoManifest, CodeBadManifest, CodeUnknownField, CodeMissingSection,
		CodeBadID, CodeDuplicateID, CodeBadVersion, CodeBadTier, CodeMissingText,
		CodeNoAgeRating, CodeBadAgeRating, CodeBadValue, CodeNoCanvas, CodeBadShape,
		CodeKeyframesUnordered, CodeBadTimeWindow, CodeZoneWithoutAppearance, CodeZoneShared,
		CodeAppearanceUnknownZone, CodeAppearanceWithoutZones, CodeUnknownAppearance,
		CodeUnknownMediaState, CodeNotInTier, CodeTierMediaMismatch, CodeMissingMedia,
		CodeHashMismatch, CodeUnlistedFile, CodeVersionTaken, CodeUnknownTargetType,
	}
}

// A LoadError is a package that cannot be read as a scenario at all; its
// Problem has the code bad_package, no_manifest or bad_manifest.
type LoadError struct {
	Problem Problem
	Err     error
}

func (e *LoadError) Error() string {
	return "scenario package: " + e.Problem.String()
}

func (e *LoadError) Unwrap() error {
	return e.Err
}

// A Package is a scenario package as Load and LoadZip read it.
type Package struct {
	Manifest Manifest
	// Files maps every file of the package except the manifest and the
	// signature, by its path with forward slashes, to its SHA-256 in lower
	// case hex.
	Files map[string]string
}

// Validate is Validate of the manifest and the files of p.
func (p *Package) Validate() []Problem {
	return Validate(p.Manifest, p.Files)
}
