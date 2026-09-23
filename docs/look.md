# The look of the admin

Every colour of the admin pages lives in one file, `internal/admin/static/tokens.css`. Change a value there and the whole interface changes with it; no page, no component and no script carries a colour of its own, and a test refuses one that tries (D-072).

The file holds two sets of the same names. The light set stands on `:root`, the dark one twice: under `prefers-color-scheme: dark` for the automatic choice, and under `[data-theme="dark"]` for the manual one, so both say exactly the same thing (D-073). Editing a token means editing it in all three blocks.

## What each token paints

| Token | What it paints |
| --- | --- |
| `--ground` | The page behind everything. |
| `--panel` | A card, a table, the header, the bar at the top. |
| `--panel-raised` | The row the mouse is over, and anything one step in front of a panel. |
| `--text` | Every word that is read. |
| `--muted` | A word beside the main one: a hint, a unit, a time. |
| `--line` | Every border and every rule between two things. |
| `--accent` | The one colour that leads the eye: links, the page you are on, the button that does the thing. |
| `--accent-text` | What is written on the accent, so a filled button stays readable. |
| `--ok` | A state that is good: approved, online, current, published. |
| `--ok-soft` | The surface a good message sits on. |
| `--warn` | A state that wants attention: pending, outdated, installing. |
| `--warn-soft` | The surface a warning sits on. |
| `--danger` | A state that is wrong: blocked, failed, refused, and the button that deletes. |
| `--danger-soft` | The surface an error sits on. |
| `--inverse-text` | What is written on `--text` itself, as a filled badge is. |
| `--shadow` | The shadow under a dialog. |
| `--stage` | The picture area of the editor and the preview, which is a picture and not a surface. |
| `--stage-text` | The note written over an empty stage. |
| `--bar` | A bar of the timeline. |
| `--bar-selected` | The bar that is selected. |
| `--mark-ground` | The square of the mark in the corner (D-074). |
| `--mark-accent` | The arc and the ring of the mark, where thesite puts its own accent. |
| `--canvas-zone` | The outline of a zone on the canvas of the editor. |
| `--canvas-zone-fill` | The fill inside that outline. |
| `--canvas-zone-selected` | The zone that is selected, and the handles of its shape. |
| `--canvas-zone-fill-selected` | The fill of the selected zone. |
| `--canvas-zone-live` | A zone that is on screen at the moment of the playhead. |
| `--canvas-label` | The name written next to a zone. |
| `--canvas-shot` | The ring of a shot that missed, in the preview. |
| `--canvas-hit` | The mark of a shot that hit. |
| `--canvas-trace` | The outline of a live zone while the preview plays. |
| `--canvas-dot` | The flash over the picture when a hit is rewarded with one. |

## The rules the tokens keep

- **Every text reaches 4.5 to 1** against the surface it is written on, in both sets. A test computes the ratio of every pair and fails below it (D-075). After changing a value, run `go test ./internal/admin -run TestBothThemesAreReadable`.
- **No state is told by colour alone.** Every badge carries its word beside its colour, so a screen in daylight, a projector and a person who does not separate red and green all read the same thing.
- **The canvas reads the tokens too.** `editor.js` and `preview.js` ask the page for the value of a custom property when they draw, so the drawing follows the theme; nothing there is a literal colour.
- **The only file with a colour beside the tokens is the mark**, `static/logo.svg`, which is the favicon a browser loads without our stylesheet. The mark inside the page is drawn from the tokens.

## Changing the look

1. Edit the values in `internal/admin/static/tokens.css`, all three blocks.
2. Run `go test ./internal/admin`, which checks that no colour escaped the file and that both sets stay readable.
3. Look at a page in light and in dark: the switch sits in the header beside the language switch.
