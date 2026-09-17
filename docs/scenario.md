# Scenario model, version 1

Status: reference for theserver, theclient and the scenario editor. This document only grows. It defines what a scenario is, how it is stored and shipped, and what a target does with it. It deliberately says nothing about how the editor looks.

## 1. What a scenario is

A scenario is everything a target needs to run one playable experience without a server: the media, the targets that appear, where they can be hit, what a hit is worth, how long the player has, what happens immediately on a hit, what happens afterwards, and the rules that turn all of it into a score. A scenario is a package: one directory or zip with a manifest and its assets, versioned, signed, installed on a target as a whole, never in parts.

Four tiers, one model:

| Tier | Principle | Typical target class |
| --- | --- | --- |
| **VIDEO** | one video, a hit layer on top; the character in the video does not react | A, B |
| **INTERACTIVE** | video with a hitbox timeline; a hit switches to a prepared reaction clip | B, C |
| **LAYERED** | background and characters are separate layers; a character reacts while the background keeps running | B, C |
| **REALTIME** | world and characters rendered live by thegame; the scenario carries rules and hit zones, the renderer carries the world | C |

The tiers share the manifest, the rules, the zones and the reactions. They differ only in the media section.

## 2. Package layout

```
<scenario-id>/
+-- manifest.toml          # everything that is not media
+-- media/                 # videos, images, sounds, layers
+-- cover.png              # shown in the admin catalogue
+-- SIGNATURE              # signature over manifest and media hashes (later)
```

`manifest.toml`, sections:

```toml
[scenario]
id          = "zombie-alley"        # stable, lowercase, no spaces
version     = 3                     # increments on every published change
tier        = "interactive"         # video | interactive | layered | realtime
title       = { en = "Zombie Alley", de = "Zombie Gasse" }
description = { en = "...", de = "..." }
age_rating  = "18"                  # the content rating, enforced by theserver
duration_s  = 90
author      = "CYB3RGUN"
licence     = "official"            # official | community | private

[display]
canvas      = { w = 1080, h = 1920 }   # the coordinate space of all zones
orientation = "portrait"
fit         = "cover"                  # how the canvas maps to the target's screen

[rules]
points_per_hit_default = 50
miss_penalty           = 0
timeout_counts_as_hit  = true        # a target not hit in time counts against the player
timeout_penalty        = 100
lives                  = 3
score_cap              = 0            # 0 means none
```

Zones, appearances, reactions and media follow; they are described in the next sections.

## 3. Zones

A zone is an area on the canvas that can be hit, with a name, a value and a shape. Shapes are in canvas coordinates, origin top left.

```toml
[[zone]]
id     = "z-head"
name   = { en = "Head", de = "Kopf" }
shape  = "polygon"                     # rect | circle | polygon
points = [[520,300],[600,300],[600,420],[520,420]]
points_value = 100
zone_class   = "head"                  # head | torso | arm | leg | object | none
```

Zones can move in time (INTERACTIVE, LAYERED): a zone carries keyframes, and the target interpolates the shape between them.

```toml
[[zone.keyframe]]
t_ms   = 0
points = [[520,300],[600,300],[600,420],[520,420]]

[[zone.keyframe]]
t_ms   = 2000
points = [[560,310],[640,310],[640,430],[560,430]]
```

A zone belongs to an appearance (section 4). Zones of the same appearance may overlap; the first in manifest order wins.

## 4. Appearances

An appearance is one target showing up: when, for how long, which zones are live, what the player must do. This is where the time window rule lives.

```toml
[[appearance]]
id        = "a-zombie-1"
t_start_ms = 3000                   # on the scenario clock
t_end_ms   = 6000                   # after this the appearance times out
zones      = ["z-head", "z-torso"]
required_hits = 1                   # hits needed to clear it
on_timeout    = "penalty"           # penalty | nothing | end
media_state   = "walk"              # which media state plays while it is up (INTERACTIVE, LAYERED)
layer         = "zombie"            # LAYERED: the character layer that state belongs to
```

In LAYERED every appearance names its layer, and `media_state` must be a state of that layer, else the package is refused with `unknown_media_state`. In INTERACTIVE there are no layers and the states of `[media.state]` count.

The target's clock starts when the scenario starts and pauses when the session pauses. Every hit event carries the scenario time, so theserver and the editor can replay a session exactly.

## 5. Reactions

Two kinds, always separate, so the feel of a shot never depends on how fast a clip can switch.

**Immediate**: fires within the same frame as the hit, from the target's own assets, regardless of tier.

```toml
[reaction.immediate]
flash        = true
impact_sound = "media/impact.ogg"
hitmarker    = "ring"                  # ring | cross | none
blood        = "media/blood-1.webm"    # optional overlay clip, alpha
```

**Follow up**: the character reacts. Depends on tier.

```toml
[[reaction.followup]]
appearance = "a-zombie-1"
zone_class = "head"
media_state = "die-head"               # INTERACTIVE, LAYERED: which clip or layer state
duration_ms = 1800
then        = "end"                    # end | back:<state> | next
```

For VIDEO there is no follow up; the hit layer alone reacts. For REALTIME the follow up is a named event the renderer maps to its own animation.

## 6. Media by tier

**VIDEO**
```toml
[media]
main = "media/main.mp4"               # H.264 or AV1, 1080x1920 for canvas portrait
```

**INTERACTIVE**: named states, each one clip; the timeline says which state is current.
```toml
[media.state.walk]    = "media/walk.mp4"
[media.state.die-head] = "media/die-head.mp4"
[media.state.die-body] = "media/die-body.mp4"
```

**LAYERED**: a background and one or more character layers with alpha; each character has states.
```toml
[media.background]
clip = "media/alley-bg.mp4"
[media.layer.zombie]
states = { walk = "media/zombie-walk.webm", die-head = "media/zombie-die-head.webm" }
z = 10
```

Each appearance belongs to one layer and plays that layer's states (section 4); a follow up of the appearance does the same.

**REALTIME**
```toml
[media.realtime]
world      = "alley"                   # a world thegame knows
characters = ["zombie-a"]
```

Codecs and containers per class are a client decision recorded in theclient's documentation. Anything the class cannot play is rejected at install time, not at play time.

## 7. Time window rule, spelled out

For every appearance:

1. At `t_start_ms` the target shows the appearance and its zones go live.
2. A hit inside a live zone with `points_value` is scored, `required_hits` decrements, immediate reaction fires, the follow up plays when `required_hits` reaches zero.
3. At `t_end_ms` with `required_hits` still above zero the appearance times out: `on_timeout` applies, and if `rules.timeout_counts_as_hit` is true the player is charged `timeout_penalty` and loses a life if lives are in use.
4. Every one of these is an event in the journal: `appear`, `hit`, `miss`, `timeout`, `clear`, with the appearance id and scenario time, so a session can be replayed and coached.

## 8. Validation

A package is valid when: the manifest parses; every referenced media file exists and its hash matches the manifest; every zone belongs to exactly one appearance; every appearance references existing zones and media states; keyframes are in time order; the canvas is set; the tier's required media section is present and no other tier's section is; `age_rating` is set. theserver validates on upload, theclient validates on install, both with the same code (shared Go package), and the editor validates before export.

## 9. Distribution

theserver stores packages in its content directory, keeps every published version, and offers them to targets over HTTPS as zip downloads with a manifest hash. A target reports which scenario versions it holds; the admin shows per target which version is installed and whether it is current. Signing of official packages comes with the content tier; the `SIGNATURE` file is reserved for it.

## 10. What the editor must let a person do

Without code: choose a tier; upload media; see the timeline; draw a zone on a paused frame; move it across time with keyframes; give it a value and a class; add an appearance with start and end; set required hits and the timeout behaviour; choose immediate reactions; assign follow up states per zone class; set the rules; preview the whole thing in the browser with the mouse as the pistol; validate; export. Every field with a label, a description on hover and a longer why. That is the scenario editor, built with the settings foundation of B06, and it is the next pass after B06.

**Implemented in S01-B08** for the tiers VIDEO and INTERACTIVE, at `/admin/editor/<draft id>`: drafts live on the server (D-041), the server assembles and publishes the package (D-042), the canvas, the timeline and the scrubber are one vendored script (D-043), the preview runs the rules of section 7 in the browser and compares them with the engine of the server (D-044), media stays as it is uploaded (D-045), and every field carries its help from one registry (D-046). LAYERED and REALTIME are read and validated but not drawn yet. The operator guide is `docs/editor.en.md` and `docs/editor.de.md`.

The editor works from the keyboard as well:

| Keys | What it does |
|------|--------------|
| Space | Play or pause |
| Left, Right | One frame back or forward |
| Shift and Left, Right | One second back or forward |
| R, C, P | Draw a rectangle, a circle, a polygon |
| Enter | Finish a polygon |
| Esc | Stop drawing and select the scenario |
| Delete | Delete the selected object, after a question |
| K | Keyframe at the playhead |
| Shift and K | Remove the keyframe at the playhead |
| A | New appearance at the playhead |
| Plus, Minus | Zoom the timeline |
| V | Check the draft |

The same table stands on the editor page itself, so nobody has to look it up here.
