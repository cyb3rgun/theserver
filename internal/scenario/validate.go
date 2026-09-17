package scenario

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidID reports whether id may name a scenario, a zone, an appearance, a
// media state, a layer, a world or a character: 1 to 64 lower case letters,
// digits, hyphens and underscores, starting with a letter or a digit.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// Validate checks a manifest and the files of its package against
// docs/scenario.md section 8 and the rules recorded in D-035. files maps
// every file of the package except the manifest and the signature to its
// SHA-256, as Package.Files does. The problems come in a stable order; none
// means the package is valid.
func Validate(m Manifest, files map[string]string) []Problem {
	c := &checker{m: m, files: files, tier: m.Scenario.Tier}
	c.tierKnown = slices.Contains(Tiers(), c.tier)
	if m.Scenario.DurationS > 0 {
		c.durationMs = m.Scenario.DurationS * 1000
	}
	if d := m.Display; d != nil && d.Canvas != nil && d.Canvas.W > 0 && d.Canvas.H > 0 {
		c.canvas = d.Canvas
	}
	for _, key := range m.unknown {
		c.add(key, CodeUnknownField, "%s is not a field of the scenario model, version %d", key, ModelVersion)
	}
	c.info()
	c.display()
	c.rules()
	c.media()
	c.zones()
	c.appearances()
	c.membership()
	c.reactions()
	c.referencedFiles()
	return c.problems
}

type checker struct {
	m          Manifest
	files      map[string]string
	tier       string
	tierKnown  bool
	durationMs int     // zero when the duration is not valid
	canvas     *Canvas // nil when the canvas is not valid
	states     map[string]bool
	problems   []Problem
}

func (c *checker) add(field, code, format string, args ...any) {
	c.problems = append(c.problems, Problem{Field: field, Code: code, Detail: fmt.Sprintf(format, args...)})
}

// moving reports whether zones move and media has states in the tier.
func (c *checker) moving() bool {
	return c.tier == TierInteractive || c.tier == TierLayered
}

func (c *checker) id(field, kind, id string) bool {
	if ValidID(id) {
		return true
	}
	c.add(field, CodeBadID, "the %s %q must be 1 to 64 lower case letters, digits, hyphens or underscores, starting with a letter or a digit", kind, id)
	return false
}

func (c *checker) oneOf(field, value string, allowed []string) {
	if !slices.Contains(allowed, value) {
		c.add(field, CodeBadValue, "%q is not one of %s", value, strings.Join(allowed, ", "))
	}
}

// text checks that a text has every language; an optional text may be left
// out as a whole.
func (c *checker) text(field string, t Text, required bool) {
	if len(t) == 0 && !required {
		return
	}
	for _, lang := range TextLanguages() {
		if strings.TrimSpace(t[lang]) == "" {
			c.add(field+"."+lang, CodeMissingText, "the text has no %s version", lang)
		}
	}
}

func (c *checker) info() {
	s := c.m.Scenario
	c.id("scenario.id", "scenario id", s.ID)
	if s.Version < 1 {
		c.add("scenario.version", CodeBadVersion, "the version must be a whole number from 1, not %d", s.Version)
	}
	if !c.tierKnown {
		c.add("scenario.tier", CodeBadTier, "the tier %q is not one of %s", s.Tier, strings.Join(Tiers(), ", "))
	}
	c.text("scenario.title", s.Title, true)
	c.text("scenario.description", s.Description, false)
	switch {
	case s.AgeRating == "":
		c.add("scenario.age_rating", CodeNoAgeRating, "the age rating is not set")
	case !slices.Contains(AgeRatings(), s.AgeRating):
		c.add("scenario.age_rating", CodeBadAgeRating, "the age rating %q is not one of %s", s.AgeRating, strings.Join(AgeRatings(), ", "))
	}
	if s.DurationS < 1 {
		c.add("scenario.duration_s", CodeBadValue, "the duration must be at least 1 second, not %d", s.DurationS)
	}
	c.oneOf("scenario.licence", s.Licence, licences)
}

func (c *checker) display() {
	d := c.m.Display
	if c.canvas == nil {
		switch {
		case d == nil:
			c.add("display.canvas", CodeNoCanvas, "the manifest has no [display] section with a canvas")
		case d.Canvas == nil:
			c.add("display.canvas", CodeNoCanvas, "the display has no canvas")
		default:
			c.add("display.canvas", CodeNoCanvas, "the canvas is %d x %d; width and height must be at least 1", d.Canvas.W, d.Canvas.H)
		}
	}
	if d == nil {
		return
	}
	c.oneOf("display.orientation", d.Orientation, orientation)
	c.oneOf("display.fit", d.Fit, fits)
}

func (c *checker) rules() {
	r := c.m.Rules
	if r == nil {
		c.add("rules", CodeMissingSection, "the manifest has no [rules] section")
		return
	}
	for _, v := range []struct {
		name  string
		value int
	}{
		{"points_per_hit_default", r.PointsPerHitDefault},
		{"miss_penalty", r.MissPenalty},
		{"timeout_penalty", r.TimeoutPenalty},
		{"lives", r.Lives},
		{"score_cap", r.ScoreCap},
	} {
		if v.value < 0 {
			c.add("rules."+v.name, CodeBadValue, "%s must not be negative, it is %d", v.name, v.value)
		}
	}
}

// media checks that the tier has its media section and no other tier's, and
// collects the media states.
func (c *checker) media() {
	if !c.tierKnown {
		return
	}
	md := c.m.Media
	present := map[string]string{}
	if md.Main != "" {
		present[TierVideo] = "media.main"
	}
	if md.State != nil {
		present[TierInteractive] = "media.state"
	}
	switch {
	case md.Background != nil:
		present[TierLayered] = "media.background"
	case md.Layer != nil:
		present[TierLayered] = "media.layer"
	}
	if md.Realtime != nil {
		present[TierRealtime] = "media.realtime"
	}
	for _, other := range Tiers() {
		if field, ok := present[other]; ok && other != c.tier {
			c.add(field, CodeTierMediaMismatch, "%s belongs to the %s tier, the scenario is %s", field, other, c.tier)
		}
	}

	c.states = map[string]bool{}
	switch c.tier {
	case TierVideo:
		if md.Main == "" {
			c.add("media.main", CodeTierMediaMismatch, "a video scenario needs its video in media.main")
		}
	case TierInteractive:
		if len(md.State) == 0 {
			c.add("media.state", CodeTierMediaMismatch, "an interactive scenario needs at least one state in [media.state]")
		}
		for _, name := range sortedKeys(md.State) {
			c.id("media.state."+name, "media state", name)
			c.states[name] = true
		}
	case TierLayered:
		if md.Background == nil || md.Background.Clip == "" {
			c.add("media.background.clip", CodeTierMediaMismatch, "a layered scenario needs a background clip")
		}
		if len(md.Layer) == 0 {
			c.add("media.layer", CodeTierMediaMismatch, "a layered scenario needs at least one character layer")
		}
		for _, layer := range sortedKeys(md.Layer) {
			field := "media.layer." + layer
			c.id(field, "layer", layer)
			if len(md.Layer[layer].States) == 0 {
				c.add(field+".states", CodeTierMediaMismatch, "the layer %s has no states", layer)
			}
			for _, name := range sortedKeys(md.Layer[layer].States) {
				c.id(field+".states."+name, "media state", name)
				c.states[name] = true
			}
		}
	case TierRealtime:
		rt := md.Realtime
		if rt == nil || rt.World == "" {
			c.add("media.realtime.world", CodeTierMediaMismatch, "a realtime scenario needs the world thegame renders")
			return
		}
		c.id("media.realtime.world", "world", rt.World)
		for i, character := range rt.Characters {
			c.id(fmt.Sprintf("media.realtime.characters[%d]", i), "character", character)
		}
	}
}

func (c *checker) zones() {
	seen := map[string]bool{}
	for i, z := range c.m.Zones {
		field := fmt.Sprintf("zone[%d]", i)
		if c.id(field+".id", "zone id", z.ID) {
			if seen[z.ID] {
				c.add(field+".id", CodeDuplicateID, "the zone id %s is used twice", z.ID)
			}
			seen[z.ID] = true
		}
		c.text(field+".name", z.Name, false)
		shapeKnown := slices.Contains(shapes, z.Shape)
		if shapeKnown {
			c.shape(field, z.Shape, z.Points, z.Radius, 0)
		} else {
			c.add(field+".shape", CodeBadShape, "the shape %q is not one of %s", z.Shape, strings.Join(shapes, ", "))
		}
		c.oneOf(field+".zone_class", z.ZoneClass, zoneClasses)

		if len(z.Keyframes) == 0 {
			continue
		}
		if c.tierKnown && !c.moving() {
			c.add(field+".keyframe", CodeNotInTier, "zones move only in the interactive and layered tiers, the scenario is %s", c.tier)
			continue
		}
		for k, kf := range z.Keyframes {
			kfield := fmt.Sprintf("%s.keyframe[%d]", field, k)
			if kf.TMs < 0 || (c.durationMs > 0 && kf.TMs > c.durationMs) {
				c.add(kfield+".t_ms", CodeBadTimeWindow, "the keyframe at %d ms lies outside the scenario, 0 to %d ms", kf.TMs, c.durationMs)
			}
			if k > 0 && kf.TMs <= z.Keyframes[k-1].TMs {
				c.add(kfield+".t_ms", CodeKeyframesUnordered, "the keyframe at %d ms does not come after the one at %d ms", kf.TMs, z.Keyframes[k-1].TMs)
			}
			if shapeKnown {
				c.shape(kfield, z.Shape, kf.Points, kf.Radius, len(z.Points))
			}
		}
	}
}

// shape checks the points of a zone or keyframe (D-035). A keyframe of a
// polygon has as many points as its zone, given as points.
func (c *checker) shape(field, shape string, points [][]int, radius int, points0 int) {
	for j, p := range points {
		if len(p) != 2 {
			c.add(fmt.Sprintf("%s.points[%d]", field, j), CodeBadShape, "a point is [x, y]; this one has %d numbers", len(p))
			return
		}
	}
	switch shape {
	case "polygon":
		switch {
		case len(points) < 3:
			c.add(field+".points", CodeBadShape, "a polygon needs at least 3 points, this one has %d", len(points))
		case points0 > 0 && len(points) != points0:
			c.add(field+".points", CodeBadShape, "a keyframe of a polygon has as many points as its zone, %d, not %d", points0, len(points))
		}
	case "rect":
		switch {
		case len(points) != 2:
			c.add(field+".points", CodeBadShape, "a rect is given by 2 opposite corners, this one has %d points", len(points))
		case points[0][0] == points[1][0] || points[0][1] == points[1][1]:
			c.add(field+".points", CodeBadShape, "the corners %v and %v of a rect must differ in x and in y", points[0], points[1])
		}
	case "circle":
		if len(points) != 1 {
			c.add(field+".points", CodeBadShape, "a circle is given by 1 centre point, this one has %d", len(points))
		}
		if radius < 1 {
			c.add(field+".radius", CodeBadShape, "a circle needs a radius of at least 1, not %d", radius)
		}
	}
	if shape != "circle" && radius != 0 {
		c.add(field+".radius", CodeBadShape, "only a circle has a radius")
	}
	if c.canvas == nil {
		return
	}
	for j, p := range points {
		if p[0] < 0 || p[1] < 0 || p[0] > c.canvas.W || p[1] > c.canvas.H {
			c.add(fmt.Sprintf("%s.points[%d]", field, j), CodeBadShape, "the point %v lies outside the canvas of %d x %d", p, c.canvas.W, c.canvas.H)
		}
	}
}

func (c *checker) appearances() {
	if len(c.m.Appearances) == 0 {
		c.add("appearance", CodeMissingSection, "the scenario has no appearance")
	}
	zones := map[string]bool{}
	for _, z := range c.m.Zones {
		zones[z.ID] = true
	}
	seen := map[string]bool{}
	for i, a := range c.m.Appearances {
		field := fmt.Sprintf("appearance[%d]", i)
		if c.id(field+".id", "appearance id", a.ID) {
			if seen[a.ID] {
				c.add(field+".id", CodeDuplicateID, "the appearance id %s is used twice", a.ID)
			}
			seen[a.ID] = true
		}
		end := "the end of the scenario"
		if c.durationMs > 0 {
			end = fmt.Sprintf("%d ms, the end of the scenario", c.durationMs)
		}
		if a.TStartMs < 0 || a.TEndMs <= a.TStartMs || (c.durationMs > 0 && a.TEndMs > c.durationMs) {
			c.add(field, CodeBadTimeWindow, "the window from %d to %d ms must start at 0 or later, end after it starts and end by %s", a.TStartMs, a.TEndMs, end)
		}
		if len(a.Zones) == 0 {
			c.add(field+".zones", CodeAppearanceWithoutZones, "the appearance %s has no zone", a.ID)
		}
		for j, zone := range a.Zones {
			if !zones[zone] {
				c.add(fmt.Sprintf("%s.zones[%d]", field, j), CodeAppearanceUnknownZone, "there is no zone %s", zone)
			}
		}
		if a.RequiredHits < 1 {
			c.add(field+".required_hits", CodeBadValue, "an appearance needs at least 1 hit to clear, not %d", a.RequiredHits)
		}
		c.oneOf(field+".on_timeout", a.OnTimeout, onTimeouts)
		if a.MediaState == "" || !c.tierKnown {
			continue
		}
		switch {
		case !c.moving():
			c.add(field+".media_state", CodeNotInTier, "an appearance names a media state only in the interactive and layered tiers, the scenario is %s", c.tier)
		case !c.states[a.MediaState]:
			c.add(field+".media_state", CodeUnknownMediaState, "the media section has no state %s", a.MediaState)
		}
	}
}

// membership checks that every zone belongs to exactly one appearance.
func (c *checker) membership() {
	owners := map[string][]string{}
	for _, a := range c.m.Appearances {
		for _, zone := range slices.Compact(slices.Sorted(slices.Values(a.Zones))) {
			owners[zone] = append(owners[zone], a.ID)
		}
	}
	for i, z := range c.m.Zones {
		field := fmt.Sprintf("zone[%d]", i)
		switch n := len(owners[z.ID]); {
		case n == 0:
			c.add(field, CodeZoneWithoutAppearance, "no appearance lists the zone %s", z.ID)
		case n > 1:
			c.add(field, CodeZoneShared, "the zone %s belongs to the appearances %s; a zone belongs to exactly one", z.ID, strings.Join(owners[z.ID], ", "))
		}
	}
}

func (c *checker) reactions() {
	if im := c.m.Reaction.Immediate; im != nil {
		c.oneOf("reaction.immediate.hitmarker", im.Hitmarker, hitmarkers)
	}
	followups := c.m.Reaction.Followups
	if len(followups) > 0 && c.tier == TierVideo {
		c.add("reaction.followup", CodeNotInTier, "a video scenario has no follow up reactions; the hit layer alone reacts")
		return
	}
	appearances := map[string]bool{}
	for _, a := range c.m.Appearances {
		appearances[a.ID] = true
	}
	seen := map[string]bool{}
	for i, f := range followups {
		field := fmt.Sprintf("reaction.followup[%d]", i)
		if !appearances[f.Appearance] {
			c.add(field+".appearance", CodeUnknownAppearance, "there is no appearance %q", f.Appearance)
		}
		c.oneOf(field+".zone_class", f.ZoneClass, zoneClasses)
		if key := f.Appearance + " " + f.ZoneClass; seen[key] {
			c.add(field, CodeDuplicateID, "a second follow up for the appearance %s and the zone class %s", f.Appearance, f.ZoneClass)
		} else {
			seen[key] = true
		}
		if f.DurationMs < 1 {
			c.add(field+".duration_ms", CodeBadValue, "a follow up lasts at least 1 ms, not %d", f.DurationMs)
		}
		state, back := strings.CutPrefix(f.Then, "back:")
		if !back && f.Then != "end" && f.Then != "next" {
			c.add(field+".then", CodeBadValue, "%q is not end, next or back:<state>", f.Then)
		}
		switch {
		case c.moving():
			if !c.states[f.MediaState] {
				c.add(field+".media_state", CodeUnknownMediaState, "the media section has no state %q", f.MediaState)
			}
			if back && !c.states[state] {
				c.add(field+".then", CodeUnknownMediaState, "the media section has no state %q to go back to", state)
			}
		case c.tier == TierRealtime:
			c.id(field+".media_state", "renderer event", f.MediaState)
			if back {
				c.id(field+".then", "renderer state", state)
			}
		}
	}
}

// referencedFiles checks the files table against the media the manifest
// names and the files the package holds (D-035): every file is listed with
// its SHA-256, and every named file is listed and present.
func (c *checker) referencedFiles() {
	type reference struct{ field, path string }
	var refs []reference
	name := func(field, path string) {
		if path != "" {
			refs = append(refs, reference{field, path})
		}
	}
	md := c.m.Media
	name("media.main", md.Main)
	for _, state := range sortedKeys(md.State) {
		name("media.state."+state, md.State[state])
	}
	if md.Background != nil {
		name("media.background.clip", md.Background.Clip)
	}
	for _, layer := range sortedKeys(md.Layer) {
		for _, state := range sortedKeys(md.Layer[layer].States) {
			name("media.layer."+layer+".states."+state, md.Layer[layer].States[state])
		}
	}
	if im := c.m.Reaction.Immediate; im != nil {
		name("reaction.immediate.impact_sound", im.ImpactSound)
		name("reaction.immediate.blood", im.Blood)
	}

	named := map[string]bool{}
	for _, ref := range refs {
		named[ref.path] = true
		_, listed := c.m.Files[ref.path]
		_, present := c.files[ref.path]
		switch {
		case !listed:
			c.add(ref.field, CodeMissingMedia, "%s is not listed in [files]", ref.path)
		case !present:
			c.add(ref.field, CodeMissingMedia, "%s is not in the package", ref.path)
		}
	}
	for _, path := range sortedKeys(c.m.Files) {
		field := fmt.Sprintf("files[%q]", path)
		want := c.m.Files[path]
		got, present := c.files[path]
		switch {
		case path == ManifestName || path == SignatureName:
			c.add(field, CodeBadValue, "%s is not listed in [files]; the manifest hash covers it", path)
		case !hashPattern.MatchString(want):
			c.add(field, CodeHashMismatch, "the hash of %s must be a SHA-256 in lower case hex, not %q", path, want)
		case !present:
			if !named[path] {
				c.add(field, CodeMissingMedia, "%s is listed but not in the package", path)
			}
		case got != want:
			c.add(field, CodeHashMismatch, "%s has the SHA-256 %s, the manifest lists %s", path, got, want)
		}
	}
	for _, path := range sortedKeys(c.files) {
		if _, listed := c.m.Files[path]; !listed && !named[path] {
			c.add("files", CodeUnlistedFile, "%s is in the package but not listed in [files]", path)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
