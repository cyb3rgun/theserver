# Decisions

Numbered newest first. Every entry names its date, the decision, the reason and the versions it pins, copied from `go.mod`. A version is never typed from memory: a dependency is added with `go get <module>@latest` and the version Go resolves is the one recorded here.

## D-055 The tag v0.2.0

- Date: 20 September 2026 (S01-B10)
- Decision: The close of S01-B10 is tagged `v0.2.0` on `main` and pushed, and theclient pins it. The minor number is raised because `pkg/journal` writes another file and takes options, and because a caller has to close it now (D-049).
- Details: The storage under `pkg/journal` changed; the calls a caller makes did not, apart from the Close a database needs. A device that is updated from `v0.1.0` keeps its events: the CBOR journal is imported on the first open (D-053).
- Reason: theclient cannot pin a moving branch, and the journal it builds on is another thing than it was in `v0.1.0`.
- Versions: none.

## D-054 Durability is tested, not assumed

- Date: 20 September 2026 (S01-B10)
- Decision: The journal proves what it promises in tests: one that appends and is never closed, and one that opens an image of the database and its write ahead log copied while the journal was open. Both find every event that was appended.
- Details: `TestJournalKeepsEventsWithoutAClose` drops the handle as a killed process drops it and opens the same files again. `TestJournalSurvivesACopiedWalFile` copies `journal.db`, `journal.db-wal` and `journal.db-shm` away while the journal is open and nothing is checkpointed, and reads the copy, which is the image a power cut leaves. `TestRunKeepsItsJournalAcrossARestart` in `internal/simtarget` is the same thing one level up: two events written while nothing was connected are replayed after a restart, and the sequence carries on. The timing of 10,000 appends is reported and never asserted, because a build machine is not a target: `go test ./pkg/journal -run Timing -v -appends=10000`.
- Reason: The reason for this pass is a target on an SD card that must keep its hits. A promise of durability that no test makes is a hope.
- Versions: none.

## D-053 The journal keeps its API and imports the old file

- Date: 20 September 2026 (S01-B10)
- Decision: `pkg/journal` keeps the calls it had: `Open`, `Append`, `Acked`, `After`, `Epoch`, `Reset`, `LastSeq` and `Pending`. A `journal.cbor` of an earlier version is read on the first open, written into the database with its epoch, its last sequence and its unacknowledged events, and renamed to `journal.cbor.imported`.
- Agreed with the architect during this pass: a database has to be closed, so `Close` is added and every caller closes; `Open` takes options, `WithSync` and `WithDeviceID`, which existing calls do not have to pass. `WithDeviceID` writes the device into the journal and refuses the journal of another device, which is the mistake a shared directory makes.
- Details: An open database cannot be removed on Windows, so the tests that hand a journal to the simulator close it; that is the whole change outside `pkg/journal`. The import refuses a file it cannot read instead of starting empty, so a device does not silently lose what it held.
- Reason: theclient builds on this package and pins a tag; a storage change must not become a rewrite on the other side.
- Versions: none.

## D-052 The device journal stores in SQLite

- Date: 20 September 2026 (S01-B10)
- Decision: `pkg/journal` keeps its events in one SQLite file, `journal.db`, in the directory it is given: WAL mode, `foreign_keys` on, `synchronous` FULL, through `modernc.org/sqlite`, which theserver already depends on. `meta(key, value)` holds the schema version, the device id, the epoch, the last sequence handed out and the last acked sequence; `events(seq INTEGER PRIMARY KEY, epoch, event_id BLOB, kind, ts_device, payload BLOB)` holds what the server has not acknowledged. An append is one insert in one transaction, an ack deletes the rows up to the acked sequence.
- Agreed with the architect during this pass: `synchronous` is FULL by default, not NORMAL, because the reason for the change is a power cut on an SD card and NORMAL in WAL mode may roll the last transactions back; `WithSync(SyncNormal)` stays for a simulator that cares more about speed than about a power cut.
- Details: The file that was rewritten in full on every append and every ack, without fsync, is gone. Reads stay in memory: the pending events are loaded at open and kept there, so a replay does not wait for a query, while the database is the truth. The pool is one connection, because one device writes one journal. What durability costs is measured on the homelab (i9-11900K, NVMe): 10,000 appends took 24.0 s with FULL, 2.402 ms each, and 1.06 s with NORMAL, 0.106 ms each.
- Reason: theclient found in its B01 that the journal rewrote one CBOR file on every change and never called fsync. That is fine for a simulator and wrong for a target that must keep hits across a power loss, which `docs/concept.md` of theclient asks for.
- Versions: `modernc.org/sqlite v1.59.0`, already in `go.mod` since S01-B02; this pass added no dependency.

## D-051 Undo and redo in the browser, a history on the server

- Date: 18 September 2026 (S01-B09)
- Decision: The editor keeps the last 50 manifest states in the browser for undo and redo, and the server keeps the last 20 changes of every draft in `scenario_draft_history`. Undo and redo happen in the browser; "restore version" from the history happens on the server.
- Details: A version is the manifest as it stood before one change, plus the merge patch that was applied, so the list can name the parts of the scenario a change touched without carrying the manifest. `GET /api/v1/drafts/{id}/history` lists them newest first, `POST /api/v1/drafts/{id}/history/{version}/restore` writes one back; the state before a restore becomes the newest version, so a restore can itself be undone. A change that moved nothing writes no version, and the history goes when the draft goes. In the browser, undo does not send the manifest as it stands, because a merge patch would leave behind whatever the older state no longer has: `editor.js` builds the patch that turns one state into the other, with a null for every key that went away. The keys are Ctrl and Z, Ctrl and Shift and Z.
- Reason: Every change of the editor saves itself at once, which was right and left no way back. Fifty states in the browser cover the wrong drag; twenty on the server cover the reload, the other machine and the person who comes back tomorrow. Keeping every version of every draft forever would be a second content store.
- Versions: none.

## D-050 A draft is locked while somebody edits it

- Date: 18 September 2026 (S01-B09)
- Decision: A draft carries `locked_by`, `locked_name` and `locked_at`. Opening the editor takes the lock for the session, the page refreshes it every minute and releases it when it is left; another admin sees who holds it and can take it over with a confirmation that logs both names.
- Details: `locked_by` is the id of the admin token, which is the identity the server compares; `locked_name` is the name that token carried when the lock was taken, so a notice can name the holder even after the token is gone. A lock lives five minutes after its last refresh, so a closed browser or a machine that went to sleep does not hold a draft forever. The lock is not only a notice: `POST` and `DELETE /api/v1/drafts/{id}/lock` and every endpoint that changes a draft, its media, its history or its publish answer 409 `draft_locked` while somebody else holds it, while reading and the check stay open. The page of somebody who does not hold the draft reads only and offers the takeover; the browser releases with `sendBeacon`, which survives a closing tab.
- Reason: Two people on one draft used to overwrite each other silently, and the draft carried nothing that could tell them. A lock that can be taken over is honest in a venue where one person may have walked away from the screen; a lock nobody can break would leave a draft stuck until the database is edited.
- Versions: none.

## D-049 Tags version the public surface

- Date: 18 September 2026 (S01-B09)
- Decision: The public packages of theserver are versioned by git tags. `v0.1.0` is tagged at the close of S01-B09 and theclient pins it. A later pass that changes anything under `pkg/` bumps the tag.
- Details: The tag is an annotated tag on `main`, pushed to origin, so `go get github.com/cyb3rgun/theserver@v0.1.0` resolves. The zero major version says what is true: the surface may still change between minor versions, and a change that breaks a caller raises the minor number while the major is zero.
- Reason: theclient cannot import a moving branch. A tag is the smallest thing that gives it something to pin, and it costs nothing while `pkg/` stays small.
- Versions: none.

## D-048 pkg/ is the public surface of theserver

- Date: 18 September 2026 (S01-B09)
- Decision: `pkg/protocol`, `pkg/scenario` and `pkg/journal` are public: theclient and the firmware tooling may import them, and their API is versioned by tags. Everything else stays under `internal/`. D-004 held that nothing is importable from outside except by a later recorded decision; this is that decision.
- Details: `pkg/protocol` is the device link envelope, its message types and its codec. `pkg/scenario` is the manifest model, the loader, the validation and the rule engine of section 7, with `pkg/scenario/scenariotest` for the fixtures. `pkg/journal` is the device side journal, taken out of `internal/simtarget`: sequence, epoch, the events the server has not acknowledged, replay after a dropped connection. The move was one commit with `git mv`, so the history of every file follows, and nothing else changed with it.
- Reason: theclient runs the same rules a target runs and speaks the same link. Writing them a second time would be two engines drifting apart, which D-044 already calls the thing to avoid. The three packages are the ones a client needs and nothing more: no store, no HTTP server, no admin pages.
- Versions: none.

## D-047 The OpenAPI description is parsed in a test

- Date: 17 September 2026 (S01-B08)
- Decision: `docs/openapi.yaml` is read by a YAML parser in a test, into a Go shape that names every field this repository writes, with unknown fields refused. A description that runs into the next key of a flow mapping, an unquoted comma for instance, fails the test.
- Details: The test also checks that every reference points at a component that exists and that every component is used, and the route coverage test reads the parsed document instead of the lines. When the test was written it found four descriptions that a comma had cut short, from S01-B04 and S01-B06; they are quoted now. A schema below `additionalProperties` is checked by hand against the same field list, because a node read inside an unmarshaler does not inherit the strict mode of the decoder.
- Reason: The file is the description of the API for everyone outside this repository. It was edited by hand in every pass, and a comma silently threw text away.
- Versions: `go.yaml.in/yaml/v3 v3.0.5`, resolved by `go get go.yaml.in/yaml/v3@latest` on 17 September 2026. It is used by the test only; nothing in the binary reads YAML.

## D-046 Every editor field carries its help, from one registry

- Date: 17 September 2026 (S01-B08)
- Decision: `internal/editor` is the registry of the fields the scenario editor shows: the place of each field in the manifest, the control it is shown with, its limits and its label, its short description and the longer why in every language of the catalogues. The property panel is built from it, and the settings page and the editor render that help with one shared template.
- Details: A key is the json path of the field with `[#]` for the index of a repeated object, and a test walks every key against `scenario.Manifest` with reflection, so a field cannot drift away from the manifest it edits. A second test fails when a language, a label, a description or a why is missing. Values that come from the draft rather than from a fixed list, the media states of a scenario for instance, are named in the field and filled by the page.
- Reason: The operator of a venue is not a programmer. The settings foundation of B06 proved that a field with a why is understood without a manual, and a scenario has more fields than the configuration.
- Versions: none.

## D-045 Media stays as it is uploaded

- Date: 17 September 2026 (S01-B08)
- Decision: theserver converts no media. The editor takes a file only when its container and its codec are ones a target plays, read from the header of the file alone, and refuses anything else at upload with a translated message.
- Agreed with the architect during this pass: a clip is MP4 with H.264 or AV1, an overlay with transparency is WebM with VP8, VP9 or AV1, a sound is Ogg with Vorbis or Opus, and a cover is PNG. Whether the browser plays the file is the practical test.
- Agreed with the architect at the close of the pass: the editor keeps the picture on the screen as `cover.png` with "This frame as cover", so a scenario built in the editor has a cover in the catalogue without a second program.
- Details: `internal/mediakind` reads the boxes of an MP4 up to the sample description, the elements of a WebM up to the track entry, the first page of an Ogg stream and the signature of a PNG. It decodes nothing and needs nothing outside the standard library. What the server cannot know without decoding, the length of a clip and its picture size, the browser measures and sends back with `PATCH /api/v1/drafts/{id}/media/{name}`; the editor takes the duration and the canvas of the scenario from the first clip.
- Reason: A venue uploads what its camera or its editing program produced. Refusing it at upload, with a sentence that says which formats are taken, is honest; transcoding on the server is a second product.
- Versions: none.

## D-044 One rule engine, written twice and compared

- Date: 17 September 2026 (S01-B08)
- Decision: The rules of `docs/scenario.md` section 7 live in `internal/scenario/rules.go` and, decision for decision, in `internal/admin/static/rules.js`. Both write the same trace for the same shots, and the preview compares them: the check button of the preview sends the shots of the run to the server and says whether the two engines agree.
- Agreed with the architect during this pass, where section 7 left room: `rules.timeout_counts_as_hit` turns timeout costs on for the whole scenario, and `on_timeout` of an appearance says whether this one costs (`penalty`), costs nothing (`nothing`) or ends the run (`end`). A follow up with `then = end` leaves the figure on its last picture until another appearance sets a state, `back:<state>` plays that state, and `next` moves the window of the next appearance that has not started to now, keeping its length. The score never falls below zero, `score_cap` above zero holds it, a miss costs `miss_penalty` points and never a life, lives are in use above zero and the last one lost ends the run, and a shot meets the first live zone in manifest order.
- Details: The trace is the journal of section 7 with two more kinds: `state`, the clip that plays from now on, and `end` with its reason. Two scripted runs on the INTERACTIVE fixture are recorded in `internal/scenario/testdata/rules/interactive.json` and compared in a Go test; the browser proof holds the JavaScript engine against the same file. Both engines round an interpolated keyframe the same way and compare a point against a shape with the same expressions, so their traces are equal and not merely similar. The Go engine is the seed theclient grows from.
- Reason: The feel of the game is decided by these rules, and a difference between what the editor shows and what a target does would be found in a venue, not here.
- Versions: none.

## D-043 One vendored editor script, no framework

- Date: 17 September 2026 (S01-B08)
- Decision: The drawing canvas, the timeline and the video scrubber of the editor are one plain JavaScript file, `internal/admin/static/editor.js`, served from the binary with a subresource integrity hash, as `settings.js` and `scenarios.js` are. The preview adds `rules.js` and `preview.js` the same way. No bundler, no npm, no CDN. HTMX renders the forms around them.
- Agreed with the architect at the close of the pass: a zone drawn in the editor starts without a name, so that an optional text is never half filled in one language; a change while a zone has keyframes writes a keyframe at the playhead, and a zone without keyframes is changed itself.
- Details: The scripts hold no text: the page carries every word they show in data attributes, so both languages come from the catalogues. A test compares the hash in the page with the hash of the file that is served. The pages keep the content security policy of B06: scripts and styles only from `/admin/static`.
- Reason: The editor has to run in a venue without an internet connection, from one binary, and a build step in the browser would be a second toolchain nobody there can repair.
- Versions: none.

## D-042 The server assembles the package

- Date: 17 September 2026 (S01-B08)
- Decision: On publish the server writes `manifest.toml` from the draft, computes the SHA-256 of every file into the `[files]` table, zips the package and hands it to the same chain an upload takes: validated by `internal/scenario`, stored by the content store, published as the next version. The editor never sees a hash.
- Agreed with the architect at the close of the pass: the version is decided at publish, one above the latest published one, and the draft stays afterwards and works towards the next version; throwing a draft away is a button on the editor page.
- Details: `scenario.WriteManifest` writes the manifest, and a test reads back every valid fixture unchanged, with the same hash. The version is one above the latest published version of the scenario, and the draft stays after a publish and works towards the next one. A draft with problems publishes nothing at all. A test compares a package from the editor with the hand made fixture it was copied from, down to the files table.
- Reason: A person who draws zones should not think about hashes, and a package from the editor must be the same kind of thing as a package from a studio, checked by the same code.
- Versions: none.

## D-041 Drafts live on the server

- Date: 17 September 2026 (S01-B08)
- Decision: A scenario in the making is a row in `scenario_drafts` with its manifest as JSON and its media below `content/drafts/<draft id>/media`, not a document in the browser. Every editor action is a request that changes the draft; closing the browser loses nothing.
- Agreed with the architect at the close of the pass: a draft is opened for the tiers video and interactive only, and another tier is refused at creation with a sentence that says so. A new draft starts at age rating 18, licence `private`, the admin who opened it as author, a canvas of 1080 by 1920 upright and the rules of the example in `docs/scenario.md`; the editor takes the duration and the canvas from the first clip it measures and, in a video scenario, makes that clip the main video. Three endpoints beside the list of the briefing carry the editor: `GET /api/v1/drafts/{id}/media/{name}`, which the video element needs, `PATCH /api/v1/drafts/{id}/media/{name}` as the second call of an upload, and `POST /api/v1/drafts/{id}/trace` for the comparison of D-044.
- Details: Migration 0007 adds the table. Twelve endpoints under `/api/v1/drafts` carry the editor: list, create (empty or as a copy of a published version), read, change, delete, upload media, read one media file, keep what the browser measured, delete media, validate, publish, and the trace of D-044. A change is a JSON merge patch (RFC 7386) on the manifest which must fit the scenario model, else nothing is stored and the field is named. The draft is kept in the shape the model writes, so nothing the model does not know survives a change.
- Reason: A venue edits a scenario over days, on whatever computer is free, and a lost evening of work is the kind of thing that makes people stop using an editor.
- Versions: none.

## D-040 Validation speaks both languages

- Date: 17 September 2026 (S01-B07)
- Decision: Every problem a scenario package can have is a code with an English and a German text in the catalogues, under `scenario.problem.<code>`. A page shows the text in its language, the field of the manifest, and the English detail below it, the way every translated API error shows its detail since this pass.
- Details: A test fails when a code has no text in a language, or a catalogue has a text for a code that does not exist. The detail stays English: it names values and paths, and the API answers in English.
- Reason: The people who upload packages are operators, not programmers; the reason a package is refused has to be readable in their language, and the exact detail has to be there for the person who fixes the package.
- Versions: none.

## D-039 The age rating is checked at assignment

- Date: 17 September 2026 (S01-B07)
- Decision: A session plays one published scenario version. It cannot be given a version whose age rating is above the age that a device of the session is set for. Until rooms carry that age, a device carries it: `devices.min_age`, one of 0, 6, 12, 16 and 18, and 18 for a new device.
- Agreed with the architect during this pass: the age is a column of the device, set with `theserver device add --min-age`, changed with `POST /api/v1/devices/{id}/min_age` and on the device page; a scenario is assigned only while its session is created.
- Details: The check runs in one transaction with the change, in three places: when a version is assigned, when a device joins a session that plays a version, and when the age of a device in a created or running session is lowered. A refusal is 409 `age_rating` and names the devices and their ages. Migration 0006 adds `devices.min_age` and `sessions.scenario_version`; `sessions.scenario` holds the id of the assigned scenario, and a label given at creation until one is assigned.
- Reason: The rating is part of the product promise; a venue must not be able to start a scenario for players it is not rated for, and the check belongs where the decision is made.
- Versions: none.

## D-038 Targets fetch content; the link only announces it

- Date: 17 September 2026 (S01-B07)
- Decision: The server never pushes media over the link. It announces a version with the command `content_available {id, ver, sha, size}`; the device downloads `GET /api/v1/scenarios/{id}/{ver}/package.zip` with its device token, resumes with `Range`, checks size, manifest hash and validation, and reports `content` events. It reports what it holds in every `health` under `scn`. Protocol section 8.10 is the reference.
- Agreed with the architect during this pass: a row of `device_scenarios` stays once a device held a version, with the time it last became current, and `current` is set while the latest report of the device includes the version. A device that is offline when a version is assigned gets the announcement after its first `health` with holdings on its next connection; a device that joins a session is announced to as well.
- Details: `sha` and `X-Manifest-SHA256` carry the manifest hash of D-036. Only approved devices download, and only published versions; admins download every version. `installing` and `failed` are journaled and logged, `failed` with the reason `e`, which the protocol adds as an optional key. The link records the holdings after the batch that carries them is stored, in journal order, and writes a health report again only when its list changed or a content report came in between. simtarget is the reference device: it takes the whole package again when a server ignores `Range`.
- Reason: Media are large and a device knows best when it can take them; the link stays small, and a download is an ordinary HTTPS request that can be resumed, cached and checked.
- Versions: none.

## D-037 Published versions never change

- Date: 17 September 2026 (S01-B07)
- Decision: A published version is never written again and never deleted in S01. A draft may be replaced by an upload of the same version or deleted. A draft with problems cannot be published.
- Agreed with the architect during this pass: the manifest carries the version. An upload whose version is above the latest published one is a draft, and replaces a draft of the same version; a version at or below the latest published one is refused with the problem `version_taken`. So put, publish and put again needs version 2 in the manifest of the second upload.
- Details: The content store checks the index before it moves a package into place and refuses a published version itself; the index refuses it once more in its own transaction. A draft whose version is at or below the latest published one, left from before the publication, cannot be published either (`version_taken`). `DELETE /api/v1/scenarios/{id}/{version}` answers 409 `published` for a published version.
- Reason: What a device installed and what a session was played with must stay reproducible; a fix is a new version.
- Versions: none.

## D-036 Packages on disk, the index in SQLite

- Date: 17 September 2026 (S01-B07)
- Decision: Every version lives in `content/<id>/<version>/package.zip` exactly as it was uploaded; the content directory is the setting `content.dir`, empty for `content` in the data directory. Migration 0005 adds the index `scenarios` (id, version, tier, title in every language, age rating, manifest hash, size, uploaded_at, uploaded_by, status draft or published, published_at, and the problems of a draft) and `device_scenarios` (device, scenario, version, installed_at, current). Media never enter the database.
- Details: An upload is written below `content/.incoming` first, read and validated there, and renamed into place only when its id and version can name it; a replaced draft is moved aside and comes back when the index refuses the new row, and whatever a crash leaves in `.incoming` is removed at the next start. A package that cannot be read, or whose id or version is unusable, is not stored and answered with 422 and its problems. The upload limit is `content.max_upload_mb`, 2048 MB by default and applied from the next upload on. The cover of the catalogue is read from the zip. The manifest hash is the SHA-256 of the canonical form of the manifest, the JSON encoding of the model under the names of the TOML file with maps in key order, behind a fixed domain string; it covers the files table and with it every file.
- Reason: Packages are large and immutable, and a zip kept byte for byte can be served with `Range`, hashed again and signed later; the index answers every question of the pages without opening a package.
- Versions: none.

## D-035 One package implements the scenario model

- Date: 17 September 2026 (S01-B07)
- Decision: `internal/scenario` is the one implementation of `docs/scenario.md`, version 1: the manifest types, reading a package from a directory or a zip, validation with typed problems (field, code, detail), and the manifest hash. It depends on nothing of the server, so theclient and the editor can use it.
- Agreed with the architect during this pass: the hash of every file is in a table `[files]`, which maps every path of the package except `manifest.toml` and `SIGNATURE` to its SHA-256 in lower case hex; a file the manifest names that is not listed is `missing_media`, a file of the package that is not listed is `unlisted_file`. Every shape is written as `points`: a polygon has at least three, a rect two opposite corners, a circle one centre point and a `radius` above 0; a keyframe carries the fields of its zone, a polygon keyframe as many points as its zone; every point lies on the canvas. `age_rating` is one of 0, 6, 12, 16 and 18, else `bad_age_rating`, and `no_age_rating` when it is missing. The example `[media.state.walk] = "..."` of the document is not TOML; the states of INTERACTIVE are read as the table `[media.state]` with `walk = "..."`.
- Details: The codes are `bad_package`, `no_manifest` and `bad_manifest` from reading, and `unknown_field`, `missing_section`, `bad_id`, `duplicate_id`, `bad_version`, `bad_tier`, `missing_text`, `no_age_rating`, `bad_age_rating`, `bad_value`, `no_canvas`, `bad_shape`, `keyframes_unordered`, `bad_time_window`, `zone_without_appearance`, `zone_shared`, `appearance_unknown_zone`, `appearance_without_zones`, `unknown_appearance`, `unknown_media_state`, `not_in_tier`, `tier_media_mismatch`, `missing_media`, `hash_mismatch`, `unlisted_file` from validation, and `version_taken` from the server. Choices of this pass that the document leaves open, to be confirmed: every id is 1 to 64 lower case letters, digits, hyphens and underscores; `orientation` is `portrait` or `landscape` and `fit` is `cover`, `contain` or `fill`; a key the model does not know is a problem; `[rules]` and at least one appearance are required; `duration_s` and `required_hits` are at least 1 and every time lies within the duration; the title needs English and German, a description or zone name that is given needs both; zones move and appearances name media states only in INTERACTIVE and LAYERED, VIDEO has no follow up, and an appearance has at most one follow up per zone class; a LAYERED state is found in any of its layers. A zip holds the package at its root or in one top directory; names that leave the package, repeat or are no regular files are refused. One fixture per code under `internal/scenario/testdata` fails with exactly that code; the hash of the INTERACTIVE fixture is pinned.
- Reason: The server, the target and the editor must agree on what a valid package is, so there is one piece of code that decides it.
- Versions: none.

## D-034 Help is part of the structure

- Date: 17 September 2026 (S01-B06)
- Decision: Every field of the settings page renders its label, a control that fits its kind (text, number with unit and range, switch, select), the current value, the value after a restart while one waits, the default, the range, the source, a short description that shows on hover and on keyboard focus, and the why in an element that expands. Each field has its own reset to the default; a save bar counts the unsaved changes; a banner lists the settings that wait for a restart. How all of it looks is the founder's, later.
- Details: The page is a plain form with post, redirect and get, so saving and resetting work without JavaScript. The count comes from `static/settings.js`, a small script of our own served with a subresource integrity hash like HTMX. Browser validation is switched off, so the reasons of a refused save come from the typed errors of the API, translated, next to each field. Settings that an environment variable or a flag sets are shown locked with the name that sets them.
- Reason: Operators have no programming knowledge. What a setting does and why it matters has to stand next to it, in their language, and the machine that renders it has to be the same for every later editor.
- Versions: none.

## D-033 Every settings change is logged

- Date: 17 September 2026 (S01-B06)
- Decision: Every change through the API or the admin page is logged as one structured line, `msg="setting changed"` with `action` (set or reset), `key`, `old`, `new`, `takes_effect` (now or after restart), `admin_token` and `admin_name`; the time is the time of the line. A value equal to the configured one is no change and is not logged. Values of sensitive settings, of which there are none yet, are written as hidden. The audit table that comes with roles reads the same event.
- Reason: Who changed what and when has to be answerable from the first editable setting on.
- Versions: none.

## D-032 Settings are written back to the configuration file

- Date: 17 September 2026 (S01-B06)
- Decision: A change on the admin page or through `PUT /api/v1/settings` is written to the TOML file the server was started with. `config.Write` regenerates the whole file from the registry: per setting its English label and description, default, range or values, environment variable, flag and restart note, the settings the file sets as values and all others commented out with their default. Tables of the file that are not theserver settings are kept with their values; comments written by hand are not. The file is replaced in one step.
- Details: `config.Runtime` applies changes all or none. The settings `log.level`, the five link timings, `admin.language` and `admin.session_hours` take effect at once: the log level through a level variable, the link timings through `link.Server.SetConfig` from the next batch, ping or connection on, the admin settings at the next page or login. `server.listen_addr`, `server.data_dir`, both TLS files, `store.busy_timeout_ms` and `log.format` take effect after a restart and are listed until then. A setting that an environment variable or a flag sets cannot be changed there, since the file would not win at the next start. Without a configuration file a change is refused with 409. A certificate without its key is refused before anything is written.
- Loading changes with it: unknown keys in a theserver section stay an error; tables that are not theserver sections are accepted and kept. For environment variables and flags only the value that wins is checked, as before, so a flag can stand in for an unusable variable.
- Changed in S01-B07, as briefed: a server started without `--config` no longer refuses a change with 409. The first change creates `theserver.toml` in the data directory that the defaults, the environment and the flags give, and logs `msg="configuration file created"` with its path; every later start without `--config` reads that file when it exists, every subcommand included. The file may name another data directory, which then holds the data, while the file stays where it was found. A file that appears there after the start is not overwritten: the change is refused with 409 `conflict` until a restart has read it.
- Reason: The admin page is the product; a setting changed there must survive a restart, and the file stays readable for someone who opens it.
- Versions: none.

## D-031 Two languages from the first form

- Date: 17 September 2026 (S01-B06)
- Decision: `internal/i18n` embeds `catalog/en.toml` and `catalog/de.toml`, read as flat keys. `T` formats a text in a language, falls back to English, renders a missing key as the key and logs a warning once. Every string of the admin pages, the notices, the login errors, the error messages of the API and the words for status, state, kind and source come from the catalogues; the texts of the settings come from the registry. The admin pages are parsed once per language.
- Details: The language of a request is the language cookie `theserver_lang`, else `admin.language`, else English. The switch in the page header sets the cookie for as long as a login lasts, returns only to admin paths, and logout clears it. German texts address the operator formally, use umlauts, and use ASCII apostrophes and hyphens only.
- Tests fail on a key missing in either catalogue, on different format verbs in a translation, on a key that a template or handler names and no catalogue holds, on loose text in a template, and on any page that asks for a missing text in either language.
- Reason: Operators work in their language from the first day; adding a language later must not mean finding strings in code.
- Versions: none. The catalogues are read with github.com/BurntSushi/toml v1.6.0, already required.

## D-030 One settings registry

- Date: 17 September 2026 (S01-B06)
- Decision: `internal/settings` declares every setting once: key, section, kind (string, int, bool, duration, enum, path, addr), default, unit, range or allowed values, whether it may be empty, whether a change needs a restart, whether it is sensitive, its serve flag, and label, description and why in English and German. The configuration loader, the configuration file, the API, the admin page and `docs/settings.md` all read it. `Parse` and `Validate` check a value per kind and answer with a typed `ValueError` whose code is one of `out_of_range`, `not_allowed`, `bad_address`, `bad_duration`, `bad_number`, `bad_bool`, `empty`, `bad_type` and `unknown_setting`.
- Details: The Config struct stays typed and is mapped onto the registry by its toml tags; a test fails when a field has no registry entry, an entry has no field, or the defaults differ. The environment variable of a setting is derived from its key, as before. A duration is a whole number of its unit, so the file keeps its numbers; text such as `2s` is accepted and converted when it is a whole number of the unit. Every number has a closed range: `store.busy_timeout_ms` 0 to 600000 ms, `link.ack_interval_ms` 1 to 10000 ms, `link.ack_batch` 1 to 1024, `link.ping_interval_s` 1 to 3600 s, `link.pong_timeout_s` and `link.hello_timeout_s` 1 to 600 s, `admin.session_hours` 1 to 720 h. New settings: `admin.language` (en or de, default en) and `admin.session_hours` (default 12). `docs/settings.md` is written by `theserver settings doc`, and a test fails when the committed file is stale.
- Reason: The admin interface is the product. Every later editor is built with this machine, and a setting described twice drifts.
- Versions: none.

## D-029 Rankings find a session by session and kind, and read unsorted

- Date: 17 September 2026 (S01-B05)
- Decision: Migration 0004 adds the index `events (session_id, kind)`. `EachEvent`, which a ranking reads through, no longer sorts; `ListEvents` keeps the order of arrival. The ranking over everything scans the journal.
- Reason: The briefing asked for indexes on `events (kind)` and `events (kind, controller_id)`. Measured on homelab (Intel Core i9-11900K) with modernc.org/sqlite and 270,000 events in 30 sessions, they made rankings slower: the planner took `events_kind` for a session ranking and read the hits of every session (22 ms became 118 ms), and the ranking over everything became slower too (538 ms to 715 ms), because hits and misses are about half the journal and an index lookup per row costs more than a scan. `events (kind, controller_id)` was used by no query. With both changes of this decision, the session ranking took 14 ms and the ranking over everything about 420 ms. A test pins the query plans.
- Agreed with the architect during this pass: `events (session_id, kind)` instead of the two briefed indexes, and no sort in `EachEvent`, which a sum does not need.
- Versions: none. SQLite 3.53.4 as built into modernc.org/sqlite v1.59.0.

## D-028 OpenAPI is written by hand

- Date: 17 September 2026 (S01-B04)
- Decision: API v1 is described in `docs/openapi.yaml`, OpenAPI 3.1, written by hand and served at `/api/v1/openapi.yaml` without a token. The binary embeds a copy in `internal/httpapi`; a test keeps the copy equal to the docs file, and a second test checks that every registered route is documented with its method and every documented operation is registered.
- Reason: The description is a product document, not generated noise, and the tests keep it from drifting away from the router.
- Refinement made during this pass: `GET /api/v1/settings` was added beside the briefed routes. The settings page needs it, and the concept asks for every setting to be reachable through the API.
- Versions: none.

## D-027 First admin page with html/template and HTMX

- Date: 17 September 2026 (S01-B04)
- Decision: The admin pages under `/admin` are server rendered with `html/template` and HTMX, embedded in the binary, with no build step. Login takes an admin token once and sets a signed session cookie for 12 hours (HttpOnly, Secure, SameSite Strict, path `/admin`), keyed by `<data_dir>/admin.key`. The pages carry one plain base stylesheet with system fonts; the visual design is the founder's and comes later.
- Reason: One binary stays one binary, and nothing on the page exists without a working API call behind it.
- Refinement agreed with the architect during this pass: the pages reach the API in process. Every page action becomes a request to the `/api/v1` handler, authorized by the admin token id behind the cookie, and the JSON answer is rendered as HTML. The API itself accepts only bearer tokens, so it has no cookie authentication and no CSRF surface; the admin pages are protected by `net/http` cross origin protection and a content security policy without inline script or style.
- Versions: HTMX 4.0.0, the newest official release on 17 September 2026 (published 28 August 2026), downloaded as the release asset `https://github.com/bigskysoftware/htmx/releases/download/v4.0.0/htmx-4.0.0-dist.zip`, 611,364 bytes, SHA-256 `858d5fb806ed3003704bc9c9a7fc7aad15213dcfc78c5b78b005fff4211ce57c`. Its `dist/htmx.min.js`, 36,716 bytes, is embedded unchanged as `internal/admin/static/htmx.min.js`, SHA-256 `e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f`, served with the subresource integrity hash `sha384-BvJpBiO8Kh31EqtJe5DRIeWrHWnCGkwytKs9NKFi86Hhw96dEqdEMzZDeK9iEGTc`. HTMX is under the Zero-Clause BSD licence, which asks for no attribution. A test checks the embedded file against the recorded SHA-256. It is never loaded from a CDN.

## D-026 A device reset is a new sequence epoch

- Date: 17 September 2026 (S01-B04)
- Decision: `devices.seq_epoch` starts at 1; events carry the epoch they were stored in, and the unique constraint is `(device_id, seq_epoch, seq)`. A reset, from `theserver device reset <id>`, the API or the admin page, moves the device to the next epoch: it starts at seq 1 again, and the events of earlier epochs stay. `LastSeq`, the contiguous ack and the handshake count within the current epoch. Migration 0003 rebuilds the events table for the new constraint and keeps every stored event in epoch 1.
- Reason: A device that lost its counter must be able to start over without deleting the journal, which is immutable (D-012).
- Refinement agreed with the architect during this pass: `welcome` carries the epoch under the key `ep` (protocol section 8.8). A reset closes the live connection with WebSocket status 1012. A device whose journal belongs to another epoch drops its unacknowledged events, starts at seq 1 in the new epoch and connects again at once, so the `last` of its next hello belongs to that epoch; simtarget does this. A connection writes only into the epoch it learned at its handshake, so events that were on their way during a reset never land in the new epoch.
- Agreed with the architect on 17 September 2026, after S01-B05: a reset drops the unacknowledged events of the old epoch, on the device and on the server. They are not renumbered into the new epoch.
- Versions: none.

## D-025 Admin API tokens

- Date: 17 September 2026 (S01-B04)
- Decision: `theserver admin token add --name <name>` prints an admin token once and stores its SHA-256 in `admin_tokens`. Every `/api/v1` request carries it as `Authorization: Bearer <token>`; none or an unknown one is answered 401. `admin token list` and `admin token revoke <id>` manage them. Passkeys and roles are S02; the table leaves room for a passkey credential beside a token.
- Reason: The API and the admin page need an operator identity now, before passkeys exist.
- Refinement agreed with the architect during this pass: `admin_tokens` has a `revoked_at` column. A revoked token keeps its row and is answered 403, which the briefing asks for; `last_used` is written at most once a minute.
- Versions: none. crypto/sha256 and crypto/subtle from the standard library.

## D-024 Only internal/scoring reads pts from payloads

- Date: 17 September 2026 (S01-B04)
- Decision: `internal/scoring` is the only package besides the device link that decodes event payloads, and it reads nothing but `pts`. The store stays payload agnostic, and the API hands payloads out base64 encoded as the device sent them.
- Reason: The payload is the device's record. Keeping its interpretation in one place keeps the rules of scoring in one place, where a later pass can recompute points under the loaded scenario.
- Versions: none.

## D-023 Rankings are computed, never stored

- Date: 17 September 2026 (S01-B04)
- Decision: A ranking is a query over the journal: hits and misses per controller, points as the sum of `pts` of the hits, scoped to one session or to everything. It is ordered by points, then hits, both descending, then controller id. Events without a controller are left out; a hit without `pts` counts as a hit worth nothing. There is no score table and no cache in S01.
- Reason: A stored score can disagree with the journal; a computed one cannot, and a replayed event is stored once, so it is counted once.
- Versions: none.

## D-022 theserver has subcommands

- Date: 16 September 2026 (S01-B03)
- Decision: The binary takes a subcommand. `serve` runs the server and is the default when none is given, so the usage of earlier passes keeps working. `device add`, `device list` and `device revoke` manage devices, and `db info` shows the state of the database. `--db-info` stays for this season as a deprecated alias of `db info`. Every subcommand reads the same layered configuration, so `--config` and `--data-dir` mean the same everywhere.
- Reason: Operator tasks that must work before the admin UI exists, above all issuing device tokens, need a home that is not a growing list of flags on the server.
- Versions: none. flag from the standard library.

## D-021 Sessions get a Go API

- Date: 16 September 2026 (S01-B03)
- Decision: The store offers CreateSession, StartSession, StopSession, AddSessionDevice, GetSession and ListSessions, plus RunningSessionFor for the device link. A session moves only forward, created to running to stopped; any other move is ErrBadTransition. welcome carries the running session of the device.
- Reason: Closes the gap from the S01-B02 handover: events referenced sessions that only raw SQL could create, and welcome needs the active session.
- Refinement agreed with the architect during this pass: an event without a session from the caller belongs to the session of its device whose started_at and ended_at cover its ts_device, start inclusive and end exclusive, the latest started if several do. A replayed event therefore lands in the session it happened in. This relies on time_mark keeping device clocks close to the server.
- Versions: none.

## D-020 Handlers leave main

- Date: 16 September 2026 (S01-B03)
- Decision: internal/httpapi owns the router, GET /healthz and the mount of the device link at /link/v1. cmd/theserver only wires configuration, store, certificate, link and lifecycle.
- Reason: Handlers in main could not be tested; the 503 path of the health endpoint, flagged in the S01-B02 handover, now has tests against a failing database and a closed store.
- Versions: none.

## D-019 Device tokens are issued from the command line in S01

- Date: 16 September 2026 (S01-B03)
- Decision: `theserver device add --id <id> --kind <kind> --class <class>` draws a token of 32 random bytes, stores only its SHA-256 hash, sets the device to approved and prints the token once with a warning that it cannot be shown again. Issuing tokens from the admin UI is B04 or later.
- Reason: The device link needs authenticated devices now, and the admin UI does not exist yet.
- Refinement agreed with the architect during this pass: the server finds the device of a bearer token through the hash of the token, before the upgrade, and the hello must then name that device, else err unauthorized. Migration 0002 adds a unique index on devices.token_hash, so no two devices share a token. The protocol keeps its single Authorization header.
- Versions: none. crypto/rand and crypto/sha256 from the standard library.

## D-018 TLS from the first start

- Date: 16 September 2026 (S01-B03)
- Decision: theserver serves HTTPS only. When `tls.cert_file` and `tls.key_file` are empty and `<data_dir>/tls/server.crt` and `server.key` do not exist, it creates a self signed certificate with the standard library: ECDSA P-256, valid for ten years, subject alternative names localhost, 127.0.0.1, ::1 and the host name. It logs the SHA-256 fingerprint on every start. Operators install a real certificate by replacing the two files or by naming others in the configuration. Devices in S01 pin the fingerprint or skip verification with an explicit flag; simtarget does the latter with `--insecure`.
- Reason: The device link carries bearer tokens, so it must never run in the clear, and a first start has to work without an operator preparing certificates.
- Versions: none. crypto/ecdsa, crypto/x509 and crypto/tls from the standard library.

## D-017 WebSocket through github.com/coder/websocket

- Date: 16 September 2026 (S01-B03)
- Decision: The device link uses github.com/coder/websocket, on the server through a net/http handler and in simtarget as the client.
- Reason: Pure Go, context aware, maintained, and it serves from a plain net/http handler.
- Refinement agreed with the architect during this pass: a device has at most one connection. A newer connection of the same device replaces the older one, which is closed without a close handshake after it has flushed what it received, so a device that lost its link gets back in without waiting for the ping timeout.
- Versions: `github.com/coder/websocket v1.8.15`, resolved with `go get github.com/coder/websocket@latest` on 16 September 2026. `go mod tidy` removed it once while nothing imported it; it was required again at exactly this version.

## D-016 CBOR through github.com/fxamacker/cbor/v2

- Date: 16 September 2026 (S01-B03)
- Decision: The messages of the device link are encoded with github.com/fxamacker/cbor/v2, written in Core Deterministic encoding (RFC 8949 section 4.2.1) and decoded with duplicate map keys refused and unknown keys ignored. An event id is a byte string of exactly 16 bytes; any other length is refused rather than padded or cut.
- Reason: Pure Go, RFC 8949, deterministic encoding, the de facto standard in Go; the same wire format is cheap to produce with tinycbor on an ESP32.
- Refinement agreed with the architect during this pass: the command id of cmd and res is an unsigned integer, counted per connection from 1.
- Versions: `github.com/fxamacker/cbor/v2 v2.9.4`, resolved with `go get github.com/fxamacker/cbor/v2@latest` on 16 September 2026, with the indirect module `github.com/x448/float16 v0.8.4`.

## D-015 Device tokens carry S01 authentication, stored as hashes

- Date: 16 September 2026 (S01-B02)
- Decision: A device authenticates in S01 with a token that the operator issues per device. The database keeps only its SHA-256 hash in devices.token_hash, never the token. Mutual TLS with device certificates replaces this in S02, without a change to the message schema.
- Reason: A stolen database must not hand out working device credentials. The column and the hashing helper exist now; issuing tokens is the admin work of B04.
- Versions: none. crypto/sha256 and crypto/subtle from the standard library.

## D-014 Timestamps are integer unix milliseconds in UTC

- Date: 16 September 2026 (S01-B02)
- Decision: Every timestamp in the database is an INTEGER holding unix milliseconds in UTC. No SQLite datetime strings anywhere.
- Reason: One representation that sorts, compares and subtracts without parsing, that an ESP32 can produce, and that carries no time zone to get wrong. The protocol already speaks unix milliseconds.
- Versions: none.

## D-013 Event ids are version 4 UUIDs, stored as 16 byte BLOBs

- Date: 16 September 2026 (S01-B02)
- Decision: An event id is a 16 byte UUID in a BLOB column, generated on the device. The store never invents one. The Go side builds ids with crypto/rand in the RFC 9562 version 4 layout and takes no uuid dependency.
- Reason: The device is the only place that knows which event this is, so the id has to travel with the event for the journal to be idempotent. Sixteen raw bytes are half the size of the text form and index as one value. The standard library covers the generation, so nothing is added to the dependency list for it.
- Versions: none of our own. github.com/google/uuid arrives as an indirect module of modernc.org/sqlite and is not used by theserver.

## D-012 Events are immutable

- Date: 16 September 2026 (S01-B02)
- Decision: The journal inserts and never updates or deletes in S01. Idempotency rests on the primary key on event_id, and a unique index on (device_id, seq) keeps a sequence number unique per device.
- Reason: An event is what a target reported at a moment; correcting it later would make the journal an opinion. Replay after a dropped connection then costs nothing: the same event id arrives, the row is already there, and nothing is written twice.
- Refinements agreed with the architect during this pass:
  - controller_id is filled by the caller, which hands the store the value it already decoded, rather than by the store decoding CBOR payloads. This pass therefore takes no CBOR dependency; the device link fills the column in B03.
  - A sequence number that another event holds is ErrSeqConflict, and an event id already stored under another device or sequence number is ErrEventConflict. Either rolls the whole batch back, so a device that contradicts itself never leaves half a batch behind.
- Versions: none.

## D-011 WAL, synchronous NORMAL, foreign keys on, busy timeout on every connection

- Date: 16 September 2026 (S01-B02)
- Decision: Every connection runs with journal_mode WAL, synchronous NORMAL, foreign_keys ON and a busy timeout, set through the DSN so that no connection of the pool can miss them. The timeout is the setting store.busy_timeout_ms, default 5000.
- Reason: WAL lets readers work while a writer holds the database, which the admin UI will need beside the event stream. synchronous NORMAL is the WAL companion that keeps commits cheap without risking the database on a crash. Foreign keys are off by default in SQLite, and the schema leans on them. The busy timeout turns a lock collision into a wait instead of an error.
- Versions: none.

## D-010 Own migration runner, no library

- Date: 16 September 2026 (S01-B02)
- Decision: Numbered SQL files under internal/store/migrations are embedded with embed, applied in ascending order, each in one transaction together with its row in schema_migrations. No migration library.
- Reason: Twenty lines of Go cover what this project needs, and they are readable in one sitting. A dependency here would have to be trusted with the schema of the journal, and it would ship in the one binary.
- Versions: none. embed from the standard library.

## D-009 SQLite through modernc.org/sqlite

- Date: 16 September 2026 (S01-B02)
- Decision: The database is SQLite, reached through the pure Go driver modernc.org/sqlite.
- Reason: No cgo, so no C toolchain on Windows or on the Raspberry, and cross compilation stays one command. No external database service, so one binary stays one binary (concept, principle 4).
- Versions: modernc.org/sqlite v1.59.0, resolved with go get modernc.org/sqlite@latest on 16 September 2026. It brings nine indirect modules, among them modernc.org/libc v1.75.7, modernc.org/memory v1.12.1, modernc.org/mathutil v1.7.1, golang.org/x/sys v0.47.0, github.com/dustin/go-humanize v1.0.1, github.com/google/uuid v1.6.0, github.com/mattn/go-isatty v0.0.24, github.com/ncruces/go-strftime v1.0.0 and github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec.

## D-008 Every pass ends with a separate docs(handover) commit

- Date: 15 September 2026 (S01-B01)
- Decision: Every pass ends with a separate `docs(handover): add <pass> handover` commit that adds `docs/handovers/<pass>.md`. The six-commit rule from S01-B01 is superseded.
- Reason: The handover reports what the pass proved, including build and run results that only exist after the last code or docs commit. A commit of its own lets the handover describe the finished state and still land in the history, instead of staying untracked or forcing a commit count that cannot hold it.
- Versions: none.

## D-007 Handover format follows THESITE season handover format

- Date: 15 September 2026 (S01-B01)
- Decision: A handover follows the shape of `THESITE/docs/season-02-handover.md`: a short state paragraph, then "Read this first", "The commands", "Where things are", "Traps that survived", "Not done, open" and "What comes next", plus any section a pass needs on top, such as choices to confirm.
- Reason: No game track handover exists on disk or in the THEGAME history to copy. THESITE's season handover is the proven format in the CYB3RGUN projects and lets the next session continue without reading the log.
- Versions: none.

## D-006 No licence file yet

- Date: 15 September 2026 (S01-B01)
- Decision: The repository carries no LICENSE file, and the README makes no licence statement.
- Reason: The licence for theserver is an open decision (concept, section 10). A file added now would claim a licence that has not been chosen.
- Versions: none.

## D-005 The docs trio, briefings and handovers live in docs/

- Date: 15 September 2026 (S01-B01)
- Decision: `docs/concept.md` grows only, `docs/decisions.md` is numbered newest first, `docs/seasons.md` holds the S track table. Briefings go to `docs/briefings/`, handovers to `docs/handovers/`.
- Reason: One fixed place for the reference, the reasons and the season state, in the same shape as the game track, so a new session finds them without being told.
- Versions: none.

## D-004 Layout by responsibility

- Date: 15 September 2026 (S01-B01)
- Decision: `cmd/theserver` is the only `main` package. Everything else lives under `internal/`, starting with `internal/config` and `internal/version`.
- Reason: Nothing leaks as a public Go API by accident; a package is made importable from outside only by a later, recorded decision.
- Versions: none. Module path `github.com/cyb3rgun/theserver`.

## D-003 TOML configuration with layered overrides

- Date: 15 September 2026 (S01-B01)
- Decision: Configuration is a TOML file read with `github.com/BurntSushi/toml`. Precedence, highest first: command line flags, environment variables prefixed `THESERVER_`, the file, built in defaults. `--write-default-config` writes every setting with its default and a one line comment.
- Reason: Comments in the file, human editable, and machine writable later by the admin UI.
- Versions: `github.com/BurntSushi/toml v1.6.0`, resolved by `go get github.com/BurntSushi/toml@latest` on 15 September 2026.

## D-002 Standard library first

- Date: 15 September 2026 (S01-B01)
- Decision: `net/http` for HTTP, `log/slog` for logging, `flag` for the command line. No web framework.
- Reason: No framework dependency for the core; pattern routing has been part of `net/http` since Go 1.22.
- Versions: standard library of the Go toolchain in D-001, no module.

## D-001 theserver always uses the newest stable Go release

- Date: 15 September 2026 (S01-B01, revised at the close of the pass)
- Decision: theserver always uses the newest stable Go release; toolchain resolved via GOTOOLCHAIN=auto. The go directive in `go.mod` names that release and is raised when a newer stable release ships.
- Reason: Patch releases carry the security and bug fixes, and a connected product has to ship them (concept, principle 8). With GOTOOLCHAIN=auto the go directive is the single statement of the toolchain: every machine builds with exactly that release, whatever Go is installed locally, and the go command fetches it on first use. The first version of this decision, Go 1.26 at its latest patch, had already fallen seven patches behind when the module was created.
- Versions: `go 1.27.1` in `go.mod`, released 1 September 2026 and the newest stable release on go.dev on 15 September 2026. `go version` in the module reports `go version go1.27.1 windows/amd64`, fetched by GOTOOLCHAIN=auto; the installed toolchain is `go1.27.0`.
- Supersedes: the first version of D-001, "Go 1.26, latest patch", with `go 1.26.1` in `go.mod` (commit `f70550b`).
