// editor.js is the drawing canvas, the timeline and the video scrubber of
// the scenario editor (D-043): one vendored file, no framework, no build
// step. The page carries the draft addresses and every text it needs in data
// attributes; this file draws, and every change it makes is a request.
(() => {
  const root = document.getElementById('editor');
  if (!root) return;

  const urls = root.dataset;

  // Every colour the canvas draws with comes from the tokens of
  // tokens.css (D-072), read from the page so that the drawing follows the
  // theme the operator chose.
  const paint = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  const words = JSON.parse(root.dataset.words || '{}');
  const tier = root.dataset.tier || 'video';
  // Zones move through time in the interactive and the layered tier only.
  // The page leaves the keyframe buttons out elsewhere; the keyboard and
  // the drag of a shape follow the same rule, so nothing here writes a
  // keyframe the check would refuse as not_in_tier.
  const zonesMove = tier === 'interactive' || tier === 'layered';
  // readOnly is a draft somebody else holds (D-050). The server refuses a
  // change to it anyway; the page stops sending, so nobody types into a
  // draft that will not keep it.
  let readOnly = root.dataset.readonly === '1';
  // lockAt is the stamp of the lock this page holds; the release carries it,
  // so a page that leaves after a newer page of the same person took the lock
  // does not take that lock away.
  let lockAt = root.dataset.lockAt || '';

  const video = document.getElementById('stage-video');
  const canvas = document.getElementById('stage-canvas');
  const ctx = canvas.getContext('2d');
  const track = document.getElementById('track');
  const timeOut = document.getElementById('stage-time');
  const stateBox = document.getElementById('editor-state');
  const empty = document.getElementById('stage-empty');

  const state = {
    draft: null,
    manifest: null,
    select: new URLSearchParams(location.search).get('select') || 'scenario',
    tool: '',
    drawing: null, // {shape, points, radius}
    drag: null, // what the mouse is moving right now
    time: 0, // the playhead in milliseconds
    pxPerMs: 0.08,
    pending: 0,
    clip: '', // the media path the video element holds
    // past and future are the manifest states of undo and redo in this
    // browser (D-051), the newest last. The server keeps its own history of
    // the last twenty changes, which survives a reload; these do not.
    past: [],
    future: [],
  };

  // UNDO_DEPTH is what D-051 asks the browser to keep.
  const UNDO_DEPTH = 50;

  // --- small helpers ---------------------------------------------------

  const clamp = (v, low, high) => Math.min(high, Math.max(low, v));
  const round = (v) => Math.round(v);

  function duration() {
    const seconds = state.manifest && state.manifest.scenario ? state.manifest.scenario.duration_s : 0;
    return Math.max(1000, (seconds || 0) * 1000);
  }

  function canvasSize() {
    const display = (state.manifest && state.manifest.display) || {};
    const size = display.canvas || {};
    return { w: size.w || 1080, h: size.h || 1920 };
  }

  function zones() {
    return (state.manifest && state.manifest.zone) || [];
  }

  function appearances() {
    return (state.manifest && state.manifest.appearance) || [];
  }

  function selection() {
    const [group, index] = state.select.split(':');
    return { group, index: index === undefined ? -1 : parseInt(index, 10) };
  }

  function busy(on, failed) {
    state.pending += on ? 1 : -1;
    if (state.pending < 0) state.pending = 0;
    if (failed) {
      stateBox.textContent = stateBox.dataset.failed;
      stateBox.classList.add('failed');
      return;
    }
    stateBox.classList.remove('failed');
    stateBox.textContent = state.pending > 0 ? stateBox.dataset.busy : stateBox.dataset.idle;
    stateBox.classList.toggle('busy', state.pending > 0);
  }

  // swap puts a fragment the server rendered into the page and tells HTMX
  // about it, so the forms inside it keep going through HTMX and not
  // through a page load.
  function swap(id, html) {
    const box = document.getElementById(id);
    if (!box) return;
    box.innerHTML = html;
    if (window.htmx && typeof window.htmx.process === 'function') window.htmx.process(box);
  }

  async function send(url, options) {
    busy(true);
    try {
      const answer = await fetch(url, Object.assign({ headers: { 'Accept': 'application/json' } }, options));
      if (!answer.ok) {
        busy(false, true);
        return null;
      }
      busy(false);
      return answer;
    } catch (err) {
      busy(false, true);
      return null;
    }
  }

  // patch sends a JSON merge patch on the manifest and takes the answer. A
  // draft this page does not hold takes none. Every change that goes through
  // remembers the state before it, so that it can be undone.
  async function patch(body, keepPanel, remember) {
    if (readOnly) return false;
    const before = state.manifest ? JSON.parse(JSON.stringify(state.manifest)) : null;
    const answer = await send(urls.patch, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!answer) return false;
    const data = await answer.json();
    if (remember !== false && before) {
      state.past.push(before);
      if (state.past.length > UNDO_DEPTH) state.past.shift();
      state.future = [];
    }
    take(data.draft);
    const stale = document.getElementById('problems-stale');
    if (stale) stale.hidden = false;
    if (!keepPanel) refreshPanel();
    refreshHistory();
    setUndo();
    return true;
  }

  // --- undo and redo ---------------------------------------------------

  // diff is the JSON merge patch (RFC 7386) that turns from into to: a key
  // that is gone becomes null, a key that changed carries its new value.
  // Undo cannot send the manifest as it stands, because a merge would leave
  // behind whatever the state it goes back to no longer has.
  function diff(from, to) {
    const out = {};
    const source = from && typeof from === 'object' ? from : {};
    const target = to && typeof to === 'object' ? to : {};
    Object.keys(source).forEach((key) => {
      if (!(key in target)) out[key] = null;
    });
    Object.keys(target).forEach((key) => {
      const a = source[key];
      const b = target[key];
      if (JSON.stringify(a) === JSON.stringify(b)) return;
      const plain = (v) => v && typeof v === 'object' && !Array.isArray(v);
      out[key] = plain(a) && plain(b) ? diff(a, b) : b;
    });
    return out;
  }

  function setUndo() {
    const undo = document.getElementById('editor-undo');
    const redo = document.getElementById('editor-redo');
    if (undo) undo.disabled = readOnly || state.past.length === 0;
    if (redo) redo.disabled = readOnly || state.future.length === 0;
  }

  // step moves one state between the two stacks and sends the difference.
  async function step(from, to) {
    if (readOnly || from.length === 0) return;
    const target = from[from.length - 1];
    const here = state.manifest ? JSON.parse(JSON.stringify(state.manifest)) : {};
    const change = diff(here, target);
    if (Object.keys(change).length === 0) {
      from.pop();
      to.push(here);
      setUndo();
      return;
    }
    if (!(await patch(change, false, false))) return;
    from.pop();
    to.push(here);
    if (to.length > UNDO_DEPTH) to.shift();
    setUndo();
  }

  const undo = () => step(state.past, state.future);
  const redo = () => step(state.future, state.past);

  async function refreshHistory() {
    if (!urls.history) return;
    const answer = await send(urls.history, { headers: { 'Accept': 'text/html' } });
    if (!answer) return;
    swap('history', await answer.text());
  }

  // take keeps a draft the server answered with.
  function take(draft) {
    state.draft = draft;
    try {
      state.manifest = JSON.parse(draft.manifest || '{}');
    } catch (err) {
      state.manifest = {};
    }
    if (typeof draft.manifest === 'object' && draft.manifest !== null) state.manifest = draft.manifest;
    setPublish(draft.problems ? draft.problems.length === 0 : false);
    chooseClip();
    drawTimeline();
    draw();
  }

  function setPublish(ready) {
    const button = document.getElementById('editor-publish');
    if (button) button.disabled = !ready;
  }

  async function load() {
    const answer = await send(urls.draft, {});
    if (!answer) return;
    take(await answer.json());
    setUndo();
    measureMedia();
  }

  // --- geometry --------------------------------------------------------

  // shapeAt is the shape of a zone at the scenario time t: its keyframes
  // interpolated, or its own points when it has none.
  function shapeAt(zone, t) {
    const frames = zone.keyframe || [];
    if (frames.length === 0) return { points: zone.points || [], radius: zone.radius || 0 };
    if (t <= frames[0].t_ms) return { points: frames[0].points || [], radius: frames[0].radius || 0 };
    const last = frames[frames.length - 1];
    if (t >= last.t_ms) return { points: last.points || [], radius: last.radius || 0 };
    for (let i = 1; i < frames.length; i++) {
      const b = frames[i];
      if (t > b.t_ms) continue;
      const a = frames[i - 1];
      const span = b.t_ms - a.t_ms;
      const at = span <= 0 ? 0 : (t - a.t_ms) / span;
      const points = (a.points || []).map((p, j) => {
        const q = (b.points || [])[j] || p;
        return [round(p[0] + (q[0] - p[0]) * at), round(p[1] + (q[1] - p[1]) * at)];
      });
      const radius = round((a.radius || 0) + ((b.radius || 0) - (a.radius || 0)) * at);
      return { points, radius };
    }
    return { points: last.points || [], radius: last.radius || 0 };
  }

  function liveAt(t) {
    const live = new Set();
    appearances().forEach((a) => {
      if (t >= a.t_start_ms && t < a.t_end_ms) (a.zones || []).forEach((z) => live.add(z));
    });
    return live;
  }

  function inside(zone, shape, x, y) {
    const points = shape.points || [];
    if (zone.shape === 'circle') {
      if (points.length < 1) return false;
      const dx = x - points[0][0];
      const dy = y - points[0][1];
      return dx * dx + dy * dy <= shape.radius * shape.radius;
    }
    if (zone.shape === 'rect') {
      if (points.length < 2) return false;
      const x1 = Math.min(points[0][0], points[1][0]);
      const x2 = Math.max(points[0][0], points[1][0]);
      const y1 = Math.min(points[0][1], points[1][1]);
      const y2 = Math.max(points[0][1], points[1][1]);
      return x >= x1 && x <= x2 && y >= y1 && y <= y2;
    }
    let hit = false;
    for (let i = 0, j = points.length - 1; i < points.length; j = i++) {
      const [xi, yi] = points[i];
      const [xj, yj] = points[j];
      if ((yi > y) !== (yj > y) && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) hit = !hit;
    }
    return hit;
  }

  // --- the canvas ------------------------------------------------------

  function fitCanvas() {
    const size = canvasSize();
    if (canvas.width !== size.w || canvas.height !== size.h) {
      canvas.width = size.w;
      canvas.height = size.h;
    }
  }

  function at(event) {
    const box = canvas.getBoundingClientRect();
    const size = canvasSize();
    return {
      x: clamp(round(((event.clientX - box.left) / box.width) * size.w), 0, size.w),
      y: clamp(round(((event.clientY - box.top) / box.height) * size.h), 0, size.h),
    };
  }

  function draw() {
    if (!state.manifest) return;
    fitCanvas();
    const size = canvasSize();
    ctx.clearRect(0, 0, size.w, size.h);
    const live = liveAt(state.time);
    const chosen = selection();
    zones().forEach((zone, i) => {
      const shape = shapeAt(zone, state.time);
      const selected = chosen.group === 'zone' && chosen.index === i;
      drawShape(zone, shape, {
        selected,
        live: live.has(zone.id),
        label: zone.id || '',
      });
    });
    if (state.drawing) {
      drawShape({ shape: state.drawing.shape }, state.drawing, { drawing: true, label: '' });
    }
    timeOut.textContent = (state.time / 1000).toFixed(2);
  }

  function drawShape(zone, shape, how) {
    const points = shape.points || [];
    if (points.length === 0) return;
    ctx.lineWidth = how.selected ? 6 : 4;
    ctx.setLineDash(how.drawing ? [12, 10] : []);
    ctx.strokeStyle = how.selected ? paint('--canvas-zone-selected')
      : how.live ? paint('--canvas-zone-live') : paint('--canvas-zone');
    ctx.fillStyle = how.selected ? paint('--canvas-zone-fill-selected') : paint('--canvas-zone-fill');
    ctx.beginPath();
    if (zone.shape === 'circle') {
      ctx.arc(points[0][0], points[0][1], Math.max(1, shape.radius || 0), 0, Math.PI * 2);
    } else if (zone.shape === 'rect' && points.length > 1) {
      ctx.rect(points[0][0], points[0][1], points[1][0] - points[0][0], points[1][1] - points[0][1]);
    } else {
      ctx.moveTo(points[0][0], points[0][1]);
      points.slice(1).forEach((p) => ctx.lineTo(p[0], p[1]));
      if (!how.drawing) ctx.closePath();
    }
    ctx.fill();
    ctx.stroke();
    if (how.label) {
      ctx.fillStyle = paint('--canvas-label');
      ctx.font = '28px system-ui, sans-serif';
      ctx.fillText(how.label, points[0][0] + 8, Math.max(30, points[0][1] - 12));
    }
    if (how.selected) {
      ctx.fillStyle = paint('--canvas-zone-selected');
      handles(zone, shape).forEach((p) => {
        ctx.beginPath();
        ctx.arc(p[0], p[1], 12, 0, Math.PI * 2);
        ctx.fill();
      });
    }
  }

  // handles are the points a person can drag on the selected zone.
  function handles(zone, shape) {
    const points = shape.points || [];
    if (zone.shape === 'circle' && points.length > 0) {
      return [points[0], [points[0][0] + (shape.radius || 0), points[0][1]]];
    }
    return points;
  }

  // --- changing zones --------------------------------------------------

  // writeShape puts a shape back into the zone: into the keyframe at the
  // playhead when the zone moves, else into the zone itself. A zone with
  // keyframes gets a new one at the playhead, which is what a person sees.
  function writeShape(index, shape) {
    const list = zones().slice();
    const zone = Object.assign({}, list[index]);
    const frames = zonesMove ? (zone.keyframe || []).slice() : [];
    if (frames.length === 0) {
      zone.points = shape.points;
      if (zone.shape === 'circle') zone.radius = shape.radius;
    } else {
      const frame = { t_ms: state.time, points: shape.points };
      if (zone.shape === 'circle') frame.radius = shape.radius;
      const at = frames.findIndex((f) => f.t_ms === state.time);
      if (at >= 0) frames[at] = frame;
      else frames.push(frame);
      frames.sort((a, b) => a.t_ms - b.t_ms);
      zone.keyframe = frames;
    }
    list[index] = zone;
    state.manifest.zone = list;
    patch({ zone: list }, true);
    draw();
    drawTimeline();
  }

  function addZone(shape, kind) {
    const list = zones().slice();
    const ids = new Set(list.map((z) => z.id));
    let n = list.length + 1;
    while (ids.has('z-' + n)) n++;
    const zone = {
      // A name that is empty in every language is no name at all: the model
      // takes a text that is there in one language and misses in another as
      // a problem, and a new zone has none yet.
      id: 'z-' + n,
      name: null,
      shape: kind,
      points: shape.points,
      radius: kind === 'circle' ? shape.radius : 0,
      points_value: null,
      zone_class: 'none',
      keyframe: [],
    };
    list.push(zone);
    state.manifest.zone = list;
    select('zone:' + (list.length - 1));
    patch({ zone: list });
  }

  function addKeyframe() {
    if (!zonesMove) return;
    const chosen = selection();
    if (chosen.group !== 'zone' || chosen.index < 0) return;
    const list = zones().slice();
    const zone = Object.assign({}, list[chosen.index]);
    const shape = shapeAt(zone, state.time);
    const frames = (zone.keyframe || []).slice();
    const frame = { t_ms: state.time, points: shape.points };
    if (zone.shape === 'circle') frame.radius = shape.radius;
    const at = frames.findIndex((f) => f.t_ms === state.time);
    if (at >= 0) frames[at] = frame;
    else frames.push(frame);
    frames.sort((a, b) => a.t_ms - b.t_ms);
    zone.keyframe = frames;
    list[chosen.index] = zone;
    state.manifest.zone = list;
    patch({ zone: list });
  }

  function deleteKeyframe() {
    if (!zonesMove) return;
    const chosen = selection();
    if (chosen.group !== 'zone' || chosen.index < 0) return;
    const list = zones().slice();
    const zone = Object.assign({}, list[chosen.index]);
    const frames = (zone.keyframe || []).filter((f) => Math.abs(f.t_ms - state.time) > 40);
    if (frames.length === (zone.keyframe || []).length) return;
    zone.keyframe = frames;
    list[chosen.index] = zone;
    state.manifest.zone = list;
    patch({ zone: list });
  }

  function addAppearance() {
    const list = appearances().slice();
    const ids = new Set(list.map((a) => a.id));
    let n = list.length + 1;
    while (ids.has('a-' + n)) n++;
    const start = clamp(state.time, 0, Math.max(0, duration() - 1000));
    list.push({
      id: 'a-' + n,
      t_start_ms: start,
      t_end_ms: clamp(start + 2000, start + 1, duration()),
      zones: [],
      required_hits: 1,
      on_timeout: 'penalty',
      media_state: '',
    });
    state.manifest.appearance = list;
    select('appearance:' + (list.length - 1));
    patch({ appearance: list });
  }

  function addFollowup() {
    const reaction = state.manifest.reaction || {};
    const list = (reaction.followup || []).slice();
    const first = appearances()[0];
    list.push({
      appearance: first ? first.id : '',
      zone_class: 'head',
      media_state: '',
      duration_ms: 1500,
      then: 'end',
    });
    reaction.followup = list;
    state.manifest.reaction = reaction;
    select('followup:' + (list.length - 1));
    patch({ reaction: reaction });
  }

  // --- canvas interaction ----------------------------------------------

  canvas.addEventListener('pointerdown', (event) => {
    if (!state.manifest) return;
    // A pointer that is not really down, as a test sends one, cannot be
    // captured; the drawing works without the capture.
    try {
      canvas.setPointerCapture(event.pointerId);
    } catch (err) {
      /* nothing to capture */
    }
    const point = at(event);
    if (state.tool) {
      startDrawing(point);
      return;
    }
    const chosen = selection();
    // The selection can point at a zone that is no longer there, right after
    // it was deleted or undone; then there is nothing to grab.
    if (chosen.group === 'zone' && chosen.index >= 0 && zones()[chosen.index]) {
      const zone = zones()[chosen.index];
      const shape = shapeAt(zone, state.time);
      const grabbed = handles(zone, shape).findIndex((p) => Math.hypot(p[0] - point.x, p[1] - point.y) < 26);
      if (grabbed >= 0) {
        state.drag = { kind: 'handle', index: chosen.index, handle: grabbed, shape: copy(shape), from: point };
        return;
      }
      if (inside(zone, shape, point.x, point.y)) {
        state.drag = { kind: 'move', index: chosen.index, shape: copy(shape), from: point };
        return;
      }
    }
    const hit = pick(point);
    select(hit === -1 ? 'scenario' : 'zone:' + hit);
  });

  canvas.addEventListener('pointermove', (event) => {
    const point = at(event);
    if (state.drawing && state.drawing.dragging) {
      dragDrawing(point);
      draw();
      return;
    }
    if (!state.drag) return;
    const zone = zones()[state.drag.index];
    const shape = copy(state.drag.shape);
    const dx = point.x - state.drag.from.x;
    const dy = point.y - state.drag.from.y;
    if (state.drag.kind === 'move') {
      shape.points = shape.points.map((p) => [p[0] + dx, p[1] + dy]);
    } else if (zone.shape === 'circle') {
      if (state.drag.handle === 0) shape.points = [[point.x, point.y]];
      else shape.radius = Math.max(1, round(Math.hypot(point.x - shape.points[0][0], point.y - shape.points[0][1])));
    } else {
      shape.points = shape.points.map((p, i) => (i === state.drag.handle ? [point.x, point.y] : p));
    }
    state.drag.current = shape;
    previewShape(state.drag.index, shape);
  });

  ['pointerup', 'pointercancel'].forEach((name) => canvas.addEventListener(name, (event) => {
    if (state.drawing && state.drawing.dragging) {
      finishDrawing();
      return;
    }
    if (!state.drag) return;
    const drag = state.drag;
    state.drag = null;
    if (drag.current) writeShape(drag.index, drag.current);
  }));

  canvas.addEventListener('dblclick', () => {
    if (state.drawing && state.drawing.shape === 'polygon') finishDrawing();
  });

  function copy(shape) {
    return { points: (shape.points || []).map((p) => [p[0], p[1]]), radius: shape.radius || 0 };
  }

  // previewShape shows a drag at once, without waiting for the server.
  function previewShape(index, shape) {
    const zone = zones()[index];
    if ((zone.keyframe || []).length === 0) {
      zone.points = shape.points;
      if (zone.shape === 'circle') zone.radius = shape.radius;
    }
    draw();
    drawPreview(zone, shape);
  }

  function drawPreview(zone, shape) {
    drawShape(zone, shape, { selected: true, label: zone.id });
  }

  function pick(point) {
    const list = zones();
    for (let i = 0; i < list.length; i++) {
      if (inside(list[i], shapeAt(list[i], state.time), point.x, point.y)) return i;
    }
    return -1;
  }

  function startDrawing(point) {
    if (state.tool === 'polygon') {
      if (!state.drawing) state.drawing = { shape: 'polygon', points: [], radius: 0 };
      state.drawing.points.push([point.x, point.y]);
      draw();
      return;
    }
    state.drawing = { shape: state.tool, points: [[point.x, point.y], [point.x, point.y]], radius: 1, dragging: true, from: point };
  }

  function dragDrawing(point) {
    if (state.drawing.shape === 'rect') {
      state.drawing.points[1] = [point.x, point.y];
      return;
    }
    state.drawing.points = [[state.drawing.from.x, state.drawing.from.y]];
    state.drawing.radius = Math.max(1, round(Math.hypot(point.x - state.drawing.from.x, point.y - state.drawing.from.y)));
  }

  function finishDrawing() {
    const drawing = state.drawing;
    state.drawing = null;
    setTool('');
    if (!drawing) return;
    if (drawing.shape === 'polygon' && drawing.points.length < 3) {
      draw();
      return;
    }
    if (drawing.shape === 'rect') {
      const [a, b] = drawing.points;
      if (Math.abs(a[0] - b[0]) < 4 || Math.abs(a[1] - b[1]) < 4) {
        draw();
        return;
      }
    }
    addZone({ points: drawing.points, radius: drawing.radius }, drawing.shape);
  }

  function setTool(tool) {
    state.tool = tool;
    document.querySelectorAll('[data-tool]').forEach((button) => {
      button.classList.toggle('active', button.dataset.tool === tool);
    });
    canvas.classList.toggle('drawing', tool !== '');
  }

  // --- the timeline ----------------------------------------------------

  function drawTimeline() {
    if (!state.manifest) return;
    const total = duration();
    const width = Math.max(track.clientWidth || 600, total * state.pxPerMs);
    const chosen = selection();
    track.innerHTML = '';
    const inner = document.createElement('div');
    inner.className = 'track-inner';
    inner.style.width = width + 'px';

    const ruler = document.createElement('div');
    ruler.className = 'ruler';
    const step = total > 60000 ? 10000 : total > 20000 ? 5000 : 1000;
    for (let t = 0; t <= total; t += step) {
      const mark = document.createElement('span');
      mark.className = 'mark';
      mark.style.left = (t / total) * 100 + '%';
      mark.textContent = (t / 1000).toFixed(0);
      ruler.appendChild(mark);
    }
    inner.appendChild(ruler);

    appearances().forEach((a, i) => {
      const row = document.createElement('div');
      row.className = 'lane';
      const bar = document.createElement('div');
      bar.className = 'bar' + (chosen.group === 'appearance' && chosen.index === i ? ' selected' : '');
      bar.style.left = (a.t_start_ms / total) * 100 + '%';
      bar.style.width = Math.max(0.5, ((a.t_end_ms - a.t_start_ms) / total) * 100) + '%';
      bar.dataset.appearance = String(i);
      bar.tabIndex = 0;
      bar.textContent = a.id || words.appearance;
      const left = document.createElement('span');
      left.className = 'grip left';
      const right = document.createElement('span');
      right.className = 'grip right';
      bar.appendChild(left);
      bar.appendChild(right);
      row.appendChild(bar);
      inner.appendChild(row);
    });

    if (chosen.group === 'zone' && chosen.index >= 0 && zones()[chosen.index]) {
      const zone = zones()[chosen.index];
      const row = document.createElement('div');
      row.className = 'lane keyframes';
      (zone.keyframe || []).forEach((frame) => {
        const mark = document.createElement('span');
        mark.className = 'keyframe';
        mark.style.left = (frame.t_ms / total) * 100 + '%';
        mark.dataset.keyframe = String(frame.t_ms);
        mark.title = zone.id + ' ' + frame.t_ms;
        row.appendChild(mark);
      });
      inner.appendChild(row);
    }

    const playhead = document.createElement('div');
    playhead.className = 'playhead';
    playhead.id = 'playhead';
    playhead.style.left = (state.time / total) * 100 + '%';
    inner.appendChild(playhead);
    track.appendChild(inner);
  }

  function movePlayhead() {
    const head = document.getElementById('playhead');
    if (head) head.style.left = (state.time / duration()) * 100 + '%';
  }

  track.addEventListener('pointerdown', (event) => {
    const bar = event.target.closest('.bar');
    const inner = track.querySelector('.track-inner');
    if (!inner) return;
    const box = inner.getBoundingClientRect();
    const time = clamp(round(((event.clientX - box.left) / box.width) * duration()), 0, duration());
    if (!bar) {
      seek(time);
      return;
    }
    const index = parseInt(bar.dataset.appearance, 10);
    select('appearance:' + index);
    const grip = event.target.classList.contains('grip') ? (event.target.classList.contains('left') ? 'left' : 'right') : 'move';
    const a = appearances()[index];
    state.drag = { kind: 'bar', index, grip, from: time, start: a.t_start_ms, end: a.t_end_ms };
    try {
      track.setPointerCapture(event.pointerId);
    } catch (err) {
      /* nothing to capture */
    }
  });

  track.addEventListener('pointermove', (event) => {
    if (!state.drag || state.drag.kind !== 'bar') return;
    const inner = track.querySelector('.track-inner');
    const box = inner.getBoundingClientRect();
    const total = duration();
    const time = clamp(round(((event.clientX - box.left) / box.width) * total), 0, total);
    const shift = time - state.drag.from;
    const list = appearances();
    const a = list[state.drag.index];
    if (state.drag.grip === 'move') {
      const span = state.drag.end - state.drag.start;
      a.t_start_ms = clamp(state.drag.start + shift, 0, total - span);
      a.t_end_ms = a.t_start_ms + span;
    } else if (state.drag.grip === 'left') {
      a.t_start_ms = clamp(state.drag.start + shift, 0, a.t_end_ms - 100);
    } else {
      a.t_end_ms = clamp(state.drag.end + shift, a.t_start_ms + 100, total);
    }
    drawTimeline();
  });

  ['pointerup', 'pointercancel'].forEach((name) => track.addEventListener(name, () => {
    if (!state.drag || state.drag.kind !== 'bar') return;
    state.drag = null;
    patch({ appearance: appearances() });
  }));

  // --- the video -------------------------------------------------------

  // chooseClip puts the clip of the moment into the video element: the main
  // video, or the state of the appearance at the playhead.
  function chooseClip() {
    const media = (state.manifest && state.manifest.media) || {};
    let path = media.main || '';
    if (tier === 'interactive') {
      const states = media.state || {};
      let current = '';
      appearances().forEach((a) => {
        if (state.time >= a.t_start_ms && a.media_state) current = a.media_state;
      });
      if (!current) {
        const names = Object.keys(states).sort();
        current = names.length > 0 ? names[0] : '';
      }
      path = states[current] || '';
    }
    empty.hidden = path !== '';
    if (path === state.clip) return;
    state.clip = path;
    if (!path) {
      video.removeAttribute('src');
      video.load();
      return;
    }
    const file = fileOf(path);
    video.src = urls.file + encodeURIComponent(file);
    video.load();
  }

  function fileOf(path) {
    const cut = path.lastIndexOf('/');
    return cut < 0 ? path : path.slice(cut + 1);
  }

  function seek(time) {
    state.time = clamp(round(time), 0, duration());
    if (video.src) {
      const at = state.time / 1000;
      if (isFinite(video.duration) && at <= video.duration) video.currentTime = at;
    }
    chooseClip();
    movePlayhead();
    draw();
  }

  video.addEventListener('timeupdate', () => {
    if (video.paused) return;
    state.time = clamp(round(video.currentTime * 1000), 0, duration());
    movePlayhead();
    draw();
  });

  // --- media -----------------------------------------------------------

  // measureMedia reads the length and the size of every clip the browser has
  // not measured yet and sends them back (D-045).
  async function measureMedia() {
    const files = (state.draft && state.draft.media) || [];
    for (const file of files) {
      if (file.duration_ms > 0 || (file.use !== 'clip' && file.use !== 'overlay')) continue;
      const measured = await measureOne(file);
      if (!measured) continue;
      const answer = await send(urls.measure + encodeURIComponent(file.name) + '/measure', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(measured),
      });
      if (!answer) continue;
      const data = await answer.json();
      take(data.draft);
      await fillFromClip(file, measured);
    }
  }

  function measureOne(file) {
    return new Promise((resolve) => {
      const probe = document.createElement('video');
      probe.preload = 'metadata';
      probe.muted = true;
      probe.addEventListener('loadedmetadata', () => {
        resolve({
          duration_ms: Math.round((probe.duration || 0) * 1000),
          width: probe.videoWidth || 0,
          height: probe.videoHeight || 0,
        });
        probe.removeAttribute('src');
      });
      probe.addEventListener('error', () => resolve(null));
      probe.src = urls.file + encodeURIComponent(file.name);
    });
  }

  // fillFromClip takes the duration and the canvas from the first clip, so
  // that a person who uploaded a video can start at once.
  async function fillFromClip(file, measured) {
    if (file.use !== 'clip' || measured.width === 0) return;
    const info = state.manifest.scenario || {};
    const display = state.manifest.display || {};
    const size = display.canvas || {};
    const change = {};
    if (!info.duration_s) change.scenario = { duration_s: Math.max(1, Math.ceil(measured.duration_ms / 1000)) };
    if (!size.w || !size.h || (size.w === 1080 && size.h === 1920 && !info.duration_s)) {
      change.display = {
        canvas: { w: measured.width, h: measured.height },
        orientation: measured.height >= measured.width ? 'portrait' : 'landscape',
      };
    }
    const media = state.manifest.media || {};
    if (tier === 'video' && !media.main) change.media = { main: file.path };
    if (Object.keys(change).length > 0) await patch(change);
  }

  // The upload runs as XHR, so a long video shows how far it is.
  function upload(form) {
    const input = form.querySelector('input[type=file]');
    if (!input || input.files.length === 0) return;
    const progress = form.querySelector('progress');
    const body = new FormData();
    body.append('file', input.files[0]);
    const request = new XMLHttpRequest();
    request.open('POST', form.getAttribute('action'));
    request.setRequestHeader('Accept', 'text/html');
    if (progress) {
      progress.hidden = false;
      progress.value = 0;
      request.upload.addEventListener('progress', (event) => {
        if (event.lengthComputable) progress.value = Math.round((event.loaded / event.total) * 100);
      });
    }
    busy(true);
    request.addEventListener('load', async () => {
      busy(false, request.status >= 400);
      swap('media', request.responseText);
      input.value = '';
      await load();
    });
    request.addEventListener('error', () => busy(false, true));
    request.send(body);
  }

  // cover keeps the picture on the screen as the cover of the package.
  function cover() {
    if (!video.src || !video.videoWidth) return;
    const shot = document.createElement('canvas');
    shot.width = video.videoWidth;
    shot.height = video.videoHeight;
    shot.getContext('2d').drawImage(video, 0, 0, shot.width, shot.height);
    shot.toBlob((blob) => {
      if (!blob) return;
      const body = new FormData();
      body.append('name', 'cover.png');
      body.append('file', blob, 'cover.png');
      const request = new XMLHttpRequest();
      request.open('POST', urls.media);
      request.setRequestHeader('Accept', 'text/html');
      busy(true);
      request.addEventListener('load', async () => {
        busy(false, request.status >= 400);
        swap('media', request.responseText);
        await load();
      });
      request.addEventListener('error', () => busy(false, true));
      request.send(body);
    }, 'image/png');
  }

  // --- the property panel ----------------------------------------------

  function select(what) {
    state.select = what;
    refreshPanel();
    drawTimeline();
    draw();
  }

  async function refreshPanel() {
    const answer = await send(urls.panel + '?select=' + encodeURIComponent(state.select), {
      headers: { 'Accept': 'text/html' },
    });
    if (!answer) return;
    swap('panel', await answer.text());
  }

  // --- the bar ---------------------------------------------------------

  async function validate() {
    const answer = await send(urls.validate, { method: 'POST', headers: { 'Accept': 'text/html' } });
    if (!answer) return;
    swap('problems', await answer.text());
    readReady();
    await load();
  }

  function readReady() {
    const mark = document.querySelector('#problems [data-ready]');
    if (mark) setPublish(mark.dataset.ready === '1');
  }

  async function publish(button) {
    if (button.dataset.confirm && !window.confirm(button.dataset.confirm)) return;
    const answer = await send(urls.publish, { method: 'POST', headers: { 'Accept': 'text/html' } });
    if (!answer) return;
    if (answer.redirected) {
      location.href = answer.url;
      return;
    }
    swap('problems', await answer.text());
    readReady();
    await load();
  }

  // --- the lock --------------------------------------------------------

  // The page took the lock when it was rendered (D-050) and keeps it alive
  // while it is open. A refusal means somebody took the draft over: the page
  // stops writing and says so, and a reload shows who has it now.
  function keepLock() {
    const every = parseInt(root.dataset.lockEvery || '0', 10);
    if (!urls.lock || readOnly || !(every > 0)) return;
    window.setInterval(async () => {
      let ok = false;
      try {
        const answer = await fetch(urls.lock, {
          method: 'POST',
          headers: { 'Accept': 'application/json' },
        });
        ok = answer.ok;
        if (ok) {
          const lock = await answer.json();
          if (lock && lock.locked_at) lockAt = String(lock.locked_at);
        }
      } catch (err) {
        ok = true; // a network hiccup is not somebody else at the draft
      }
      if (ok) return;
      readOnly = true;
      setUndo();
      stateBox.textContent = words.lost || stateBox.dataset.failed;
      stateBox.classList.add('failed');
    }, every);
  }

  // The lock is released when the page goes. sendBeacon survives a closing
  // tab, which fetch does not; without it the draft would stay locked until
  // the lock stopped being refreshed.
  function releaseLock() {
    if (!urls.unlock || readOnly) return;
    const url = urls.unlock + (lockAt ? '?at=' + encodeURIComponent(lockAt) : '');
    if (navigator.sendBeacon) navigator.sendBeacon(url, new Blob([], { type: 'text/plain' }));
    else fetch(url, { method: 'POST', keepalive: true }).catch(() => {});
  }

  window.addEventListener('pagehide', releaseLock);

  // The property panel changes the manifest through HTMX, not through patch,
  // so the state before such a change is put on the undo stack when its form
  // goes. Without this, a field or a delete from the panel would be the one
  // change undo could not take back.
  function remember() {
    if (readOnly || !state.manifest) return;
    state.past.push(JSON.parse(JSON.stringify(state.manifest)));
    if (state.past.length > UNDO_DEPTH) state.past.shift();
    state.future = [];
    setUndo();
  }

  // --- events ----------------------------------------------------------

  document.addEventListener('click', (event) => {
    const tool = event.target.closest('[data-tool]');
    if (tool) {
      setTool(state.tool === tool.dataset.tool ? '' : tool.dataset.tool);
      state.drawing = null;
      draw();
      return;
    }
    const act = event.target.closest('[data-act]');
    if (act) {
      run(act.dataset.act, act);
      return;
    }
    const jump = event.target.closest('[data-select]');
    if (jump) {
      select(jump.dataset.select);
      return;
    }
    if (event.target.id === 'editor-validate') validate();
    if (event.target.id === 'editor-publish') publish(event.target);
    if (event.target.id === 'editor-undo') undo();
    if (event.target.id === 'editor-redo') redo();
  });

  function run(what) {
    switch (what) {
      case 'play':
        if (video.paused) video.play().catch(() => {});
        else video.pause();
        break;
      case 'back':
        seek(state.time - 40);
        break;
      case 'forward':
        seek(state.time + 40);
        break;
      case 'keyframe':
        addKeyframe();
        break;
      case 'keyframe-delete':
        deleteKeyframe();
        break;
      case 'appearance':
        addAppearance();
        break;
      case 'followup':
        addFollowup();
        break;
      case 'cover':
        cover();
        break;
      case 'zoom-in':
        state.pxPerMs = Math.min(2, state.pxPerMs * 1.5);
        drawTimeline();
        break;
      case 'zoom-out':
        state.pxPerMs = Math.max(0.01, state.pxPerMs / 1.5);
        drawTimeline();
        break;
      default:
        break;
    }
  }

  // Forms with a question are asked before they go.
  document.addEventListener('submit', (event) => {
    const form = event.target;
    if (form.dataset.upload) {
      event.preventDefault();
      upload(form);
      return;
    }
    const button = event.submitter;
    const question = (button && button.dataset.confirm) || form.dataset.confirm;
    if (question && !window.confirm(question)) {
      event.preventDefault();
      return;
    }
    if (form.closest && form.closest('#panel')) remember();
  });

  // HTMX swapped a form in; the draft it changed is read again.
  document.body.addEventListener('draft-changed', () => {
    load();
    readReady();
    refreshHistory();
  });

  // A version was restored: what is on the screen is that state, and the way
  // back is the newest entry of the history, not the stack of this browser.
  document.body.addEventListener('draft-restored', () => {
    state.past = [];
    state.future = [];
    setUndo();
    load();
    readReady();
  });

  document.addEventListener('keydown', (event) => {
    const tag = (event.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'select' || tag === 'textarea' || event.target.isContentEditable) return;
    if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'z') {
      if (event.shiftKey) redo();
      else undo();
      event.preventDefault();
      return;
    }
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    const step = event.shiftKey ? 1000 : 40;
    switch (event.key) {
      case ' ':
        run('play');
        break;
      case 'ArrowLeft':
        seek(state.time - step);
        break;
      case 'ArrowRight':
        seek(state.time + step);
        break;
      case 'r':
        setTool('rect');
        break;
      case 'c':
        setTool('circle');
        break;
      case 'p':
        setTool('polygon');
        break;
      case 'Enter':
        if (state.drawing) finishDrawing();
        break;
      case 'Escape':
        state.drawing = null;
        setTool('');
        select('scenario');
        break;
      case 'k':
        if (event.shiftKey) deleteKeyframe();
        else addKeyframe();
        break;
      case 'a':
        addAppearance();
        break;
      case 'v':
        validate();
        break;
      case '+':
        run('zoom-in');
        break;
      case '-':
        run('zoom-out');
        break;
      default:
        return;
    }
    event.preventDefault();
  });

  window.addEventListener('resize', drawTimeline);

  // The rules engine of the preview reads this state as well.
  window.cyb3rgunEditor = state;

  keepLock();
  load();
})();
