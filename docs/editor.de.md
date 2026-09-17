# Der Szenario-Editor

Eine kurze Anleitung für die Person, die ein Szenario baut. Nichts davon braucht einen Texteditor, ein Zip-Programm oder eine Kommandozeile: Alles geschieht im Browser, auf `/admin/scenarios` und `/admin/editor`. Die englische Fassung dieser Anleitung ist `docs/editor.en.md`.

Was ein Szenario ist und was ein Paket enthält, steht in `docs/scenario.md`. Diese Anleitung beschreibt den Editor von S01, der die Stufen VIDEO und INTERAKTIV baut.

## 1. Einen Entwurf anlegen

Auf der Szenarioseite (`/admin/scenarios`) gibt es zwei Wege hinein:

- **Neues Szenario.** Kennung, Titel und Stufe angeben. Die Kennung ist der Name, unter dem die Ziele das Szenario behalten; Kleinbuchstaben, Ziffern, Bindestriche und Unterstriche, zum Beispiel `zombie-alley`. Der Editor öffnet einen leeren Entwurf.
- **Als neue Version bearbeiten.** Auf der Seite eines Szenarios trägt jede veröffentlichte Version diesen Knopf. Manifest und Medien dieser Version werden in einen neuen Entwurf kopiert; veröffentlichen ergibt die nächste Version.

Ein Entwurf liegt auf dem Server. Den Browser zu schließen verliert nichts, und die Liste der offenen Entwürfe steht auf der Szenarioseite.

## 2. Die Seite

```
+--------------------------------------------------------------+
| Zustand  Prüfen  Veröffentlichen  Vorschau  Löschen           |
+---------------------------------+----------------------------+
|                                 |  Teile des Szenarios       |
|      der Clip mit den Zonen     |  Szenario Bild Regeln      |
|      darüber                    |  Medien Sofortreaktion     |
|                                 |  Zone: z-head z-torso      |
|  Play  Bild  Bild  Zeit         |  Auftritt: a-zombie-1 +    |
|  Rechteck Kreis Vieleck Key     |  Folgereaktion: ...     +  |
+---------------------------------+  ------------------------  |
| Zeitleiste: Auftritte als       |  die Felder des gewählten  |
| Balken, Keyframes als Marken    |  Objekts, jedes mit Hilfe  |
+---------------------------------+----------------------------+
| Medien: hochladen, Liste        | Prüfung: die Probleme      |
+--------------------------------------------------------------+
```

Jedes Feld trägt seine Bezeichnung, ein Fragezeichen mit einer kurzen Erklärung beim Darüberfahren und darunter ein **Warum das zählt**. Es gibt keinen Speichern-Knopf: Jede Änderung wird sofort gespeichert, und die Anzeige in der Leiste zeigt ein laufendes Speichern.

## 3. Der Weg vom Nichts zum veröffentlichten Szenario

1. **Video hochladen.** Im Medienbereich die Datei wählen und hochladen. Ein Clip ist ein MP4 mit H.264 oder AV1; ein Overlay mit Transparenz ist ein WebM, ein Ton ein Ogg, das Titelbild ein PNG. Es wird nichts umgewandelt; eine Datei in einem anderen Format wird mit einem Satz abgewiesen, der sagt, welche Formate genommen werden.
2. Der Editor misst den Clip im Browser und übernimmt Länge und Bildgröße daraus: die Dauer des Szenarios und die Fläche, in der die Zonen liegen. In einem VIDEO-Szenario wird er außerdem das Hauptvideo. In einem INTERAKTIVEN Szenario geben Sie jedem Clip in den Medienfeldern des Eigenschaftsbereichs einen Zustandsnamen, zum Beispiel `walk`, `die-head` und `die-body`.
3. **Zonen zeichnen.** Das Bild dort anhalten, wo das Ziel steht, Rechteck, Kreis oder Vieleck wählen und zeichnen. Ein Vieleck braucht einen Klick je Punkt und wird mit der Eingabetaste oder einem Doppelklick abgeschlossen. Eine ausgewählte Zone lässt sich verschieben, an ihren Griffen verformen und mit Name, Art und Wert versehen.
4. **Eine Zone durch die Zeit bewegen.** Zu einem anderen Moment gehen, die Zone dort hinschieben: Der Editor schreibt einen Keyframe an der Abspielmarke. Zwischen zwei Keyframes bewegt sich die Zone von selbst; die Marken unter der Zeitleiste zeigen, wo ihre Keyframes liegen.
5. **Auftritte anlegen.** Ein Auftritt ist ein Ziel, das auftaucht: wann es kommt, wann es weg ist, welche Zonen scharf sind, solange es steht, wie viele Treffer es erledigen, was bei Zeitablauf geschieht und welcher Clip spielt. Den Balken auf der Zeitleiste ziehen verschiebt ihn, seine Enden ändern das Zeitfenster.
6. **Reaktionen setzen.** Die Sofortreaktion ist Blitz, Ton und Treffermarke und gilt für das ganze Szenario. Eine Folgereaktion gehört zu einem Auftritt und einer Zonenart: Ein Kopftreffer darf einen anderen Clip starten als ein Rumpftreffer.
7. **Regeln setzen.** Punkte je Treffer, Abzüge, Leben, Deckel. Jedes Feld sagt, was es für ein Spiel bedeutet.
8. **Vorschau.** Die Vorschau spielt den Entwurf mit der Maus als Pistole und zählt Punkte, Treffer, Fehlschüsse, Zeitabläufe und Leben. Ihr Prüfknopf schickt die Schüsse des Laufs auch durch die Regeln des Servers und sagt, ob beide übereinstimmen; das sollten sie immer, und ein Unterschied ist ein Fehler, den man melden sollte.
9. **Prüfen und veröffentlichen.** Die Prüfung benennt jedes Problem in Ihrer Sprache, mit dem Objekt, zu dem es gehört; ein Klick auf das Objekt öffnet es im Eigenschaftsbereich. Solange ein Problem besteht, bleibt der Veröffentlichen-Knopf gesperrt. Eine veröffentlichte Version wird nie wieder geändert: Die nächste Änderung wird die nächste Version.

## 4. Tastatur

| Tasten | Wirkung |
|--------|---------|
| Leertaste | Abspielen oder anhalten |
| Links, Rechts | Ein Bild zurück oder vor |
| Umschalt und Links, Rechts | Eine Sekunde zurück oder vor |
| R, C, P | Rechteck, Kreis, Vieleck zeichnen |
| Eingabe | Ein Vieleck abschließen |
| Esc | Zeichnen abbrechen und das Szenario auswählen |
| Entf | Das ausgewählte Objekt löschen, nach Rückfrage |
| K | Keyframe an der Abspielmarke |
| Umschalt und K | Den Keyframe an der Abspielmarke entfernen |
| A | Neuer Auftritt an der Abspielmarke |
| Plus, Minus | Zeitleiste vergrößern oder verkleinern |
| V | Den Entwurf prüfen |

Dieselbe Tabelle steht auf der Editorseite selbst unter **Tastatur**.

## 5. Was der Editor in S01 nicht tut

- Er baut die Stufen VIDEO und INTERAKTIV. EBENEN und ECHTZEIT liest und prüft theserver, gezeichnet werden sie hier noch nicht.
- Er wandelt nichts um. Eine Datei, die ein Ziel nicht abspielen kann, muss vor dem Hochladen umgewandelt werden.
- Er hat kein Rückgängig. Eine Änderung wird sofort gespeichert; eine falsche Änderung wird durch die nächste korrigiert, und eine veröffentlichte Version bleibt ohnehin, wie sie ist.
