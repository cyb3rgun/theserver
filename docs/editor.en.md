# The scenario editor

A short guide for the person who builds a scenario. Nothing here needs a text editor, a zip program or a command line: everything happens in the browser, on `/admin/scenarios` and `/admin/editor`. The German version of this guide is `docs/editor.de.md`.

What a scenario is, and what a package holds, is `docs/scenario.md`. This guide is about the editor of S01, which builds the tiers VIDEO and INTERACTIVE.

## 1. Open a draft

On the scenario page (`/admin/scenarios`) there are two ways in:

- **New scenario.** Give an id, a title and a tier. The id is the name the targets keep the scenario under; lower case letters, digits, hyphens and underscores, for instance `zombie-alley`. The editor opens on an empty draft.
- **Edit as a new version.** On the page of a scenario, every published version has this button. The manifest and the media of that version are copied into a new draft, and publishing it makes the next version.

A draft lives on the server. Closing the browser loses nothing, and the list of open drafts stands on the scenario page.

## 2. The page

```
+--------------------------------------------------------------+
| state   Check   Publish   Preview   Delete draft              |
+---------------------------------+----------------------------+
|                                 |  Parts of the scenario     |
|      the clip with the zones    |  Scenario Picture Rules    |
|      drawn over it              |  Media Immediate           |
|                                 |  Zone: z-head z-torso      |
|  play  frame  frame  time       |  Appearance: a-zombie-1 +  |
|  rect circle polygon keyframe   |  Follow up: a-zombie-1 +   |
+---------------------------------+  ------------------------  |
| Timeline: appearances as bars,  |  the fields of the chosen  |
| keyframes as marks, playhead    |  object, each with help    |
+---------------------------------+----------------------------+
| Media: upload, list, delete     | Check: the problems        |
+--------------------------------------------------------------+
```

Every field carries its label, a question mark with a short description on hover, and a **Why this matters** below it. There is no save button: every change is saved at once, and the indicator in the bar shows a save that is running.

## 3. The way from nothing to a published scenario

1. **Upload the video.** In the media panel, choose the file and upload it. A clip is an MP4 with H.264 or AV1; an overlay with transparency is a WebM, a sound an Ogg, the cover a PNG. Nothing is converted; a file in another format is refused with a sentence that says which formats are taken.
2. The editor measures the clip in the browser and takes the length and the picture size from it: the duration of the scenario and the canvas the zones live in. In a VIDEO scenario it also becomes the main video. In an INTERACTIVE scenario, give each clip a state name in the media fields of the property panel, for instance `walk`, `die-head` and `die-body`.
3. **Draw the zones.** Pause the picture where the target is, choose rectangle, circle or polygon, and draw. A polygon takes a click per point and is finished with Enter or a double click. Select a zone to move it, to drag its handles, and to give it a name, a class and a value.
4. **Move a zone through time.** Go to another moment, move the zone there, and the editor writes a keyframe at the playhead. Between two keyframes the zone moves by itself; the marks under the timeline show where its keyframes are.
5. **Add the appearances.** An appearance is one target showing up: when it comes, when it is gone, which zones are live while it stands, how many hits clear it, what happens when the time runs out, and which clip plays. Drag the bar on the timeline to move it, drag its ends to change the window.
6. **Set the reactions.** The immediate reaction is the flash, the sound and the hit marker, and it belongs to the whole scenario. A follow up belongs to one appearance and one zone class: a head shot can start another clip than a torso shot.
7. **Set the rules.** Points per hit, penalties, lives, the cap. Every field says what it does to a game.
8. **Preview.** The preview plays the draft with the mouse as the pistol and counts points, hits, misses, timeouts and lives. Its check button plays the shots of the run through the rules of the server as well and says whether both agree; they always should, and a difference is a bug worth reporting.
9. **Check and publish.** The check names every problem in your language, with the object it belongs to; clicking the object opens it in the property panel. While there is a problem, the publish button stays closed. A published version is never changed again: the next change becomes the next version.

## 4. Keyboard

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

The same table stands on the editor page itself, under **Keyboard**.

## 5. What the editor does not do in S01

- It builds the tiers VIDEO and INTERACTIVE. LAYERED and REALTIME are read and validated by theserver, but they are not drawn here yet.
- It converts nothing. A file that a target cannot play has to be converted before it is uploaded.
- It has no undo. A change is saved at once; a wrong change is corrected by the next one, and a published version stays as it is in any case.
