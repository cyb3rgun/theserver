// rules.js is the rule engine of docs/scenario.md section 7 in the browser
// (D-044): the same decisions as pkg/scenario/rules.go, in the same
// order, with the same numbers. The preview plays with it, and the browser
// proof compares its trace with the trace of the Go engine on the same
// fixture; a difference between the two is a bug in one of them.
//
// Nothing here talks to the server and nothing here draws. It holds no text,
// so it needs no translation.
(() => {
  const EVENT = {
    appear: 'appear',
    hit: 'hit',
    miss: 'miss',
    timeout: 'timeout',
    clear: 'clear',
    state: 'state',
    end: 'end',
  };
  const END = { done: 'done', timeout: 'timeout', lives: 'lives' };

  // shapeAt is the shape of a zone at the scenario time t: its keyframes
  // interpolated and rounded, or its own points when it has none. The Go
  // engine rounds the same way.
  function shapeAt(zone, t) {
    const frames = zone.keyframe || [];
    if (frames.length === 0) return { points: zone.points || [], radius: zone.radius || 0 };
    if (t <= frames[0].t_ms) return { points: frames[0].points || [], radius: frames[0].radius || 0 };
    const last = frames[frames.length - 1];
    if (t >= last.t_ms) return { points: last.points || [], radius: last.radius || 0 };
    let index = 0;
    while (index < frames.length && frames[index].t_ms < t) index++;
    const b = frames[index];
    const a = frames[index - 1];
    const span = b.t_ms - a.t_ms;
    const at = span > 0 ? (t - a.t_ms) / span : 0;
    const points = (a.points || []).map((p, j) => {
      const q = (b.points || [])[j] || p;
      return [Math.round(p[0] + (q[0] - p[0]) * at), Math.round(p[1] + (q[1] - p[1]) * at)];
    });
    const radius = Math.round((a.radius || 0) + ((b.radius || 0) - (a.radius || 0)) * at);
    return { points, radius };
  }

  // inShape reports whether a point lies in a shape; the edge counts as
  // inside.
  function inShape(shape, points, radius, x, y) {
    if (shape === 'circle') {
      if (points.length < 1) return false;
      const dx = x - points[0][0];
      const dy = y - points[0][1];
      return dx * dx + dy * dy <= radius * radius;
    }
    if (shape === 'rect') {
      if (points.length < 2) return false;
      const x1 = Math.min(points[0][0], points[1][0]);
      const x2 = Math.max(points[0][0], points[1][0]);
      const y1 = Math.min(points[0][1], points[1][1]);
      const y2 = Math.max(points[0][1], points[1][1]);
      return x >= x1 && x <= x2 && y >= y1 && y <= y2;
    }
    let hit = false;
    for (let i = 0, j = points.length - 1; i < points.length; j = i, i++) {
      const xi = points[i][0];
      const yi = points[i][1];
      const xj = points[j][0];
      const yj = points[j][1];
      if ((yi > y) !== (yj > y) && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) hit = !hit;
    }
    return hit;
  }

  // A Run is one play of a scenario: the state of the rules while it runs.
  class Run {
    constructor(manifest) {
      this.m = manifest || {};
      this.rules = this.m.rules || {};
      this.durationMs = ((this.m.scenario || {}).duration_s || 0) * 1000;
      this.appearances = this.m.appearance || [];
      this.zones = this.m.zone || [];
      this.followups = ((this.m.reaction || {}).followup) || [];
      this.startAt = this.appearances.map((a) => a.t_start_ms);
      this.endAt = this.appearances.map((a) => a.t_end_ms);
      this.left = this.appearances.map((a) => a.required_hits);
      this.done = this.appearances.map(() => false);
      this.started = this.appearances.map(() => false);
      this.playing = -1;
      this.playUntil = 0;
      this.state = '';
      this.score = 0;
      this.hits = 0;
      this.misses = 0;
      this.timeouts = 0;
      this.lives = this.rules.lives || 0;
      this.events = [];
      this.ended = false;
      this.endedMs = 0;
      this.now = 0;
    }

    // add writes one event, leaving out what the Go engine leaves out.
    add(event) {
      const out = { t_ms: event.t_ms, kind: event.kind };
      ['appearance', 'zone', 'zone_class', 'state', 'reason'].forEach((key) => {
        if (event[key]) out[key] = event[key];
      });
      if (event.points) out.points = event.points;
      out.score = this.score;
      out.lives = this.lives;
      this.events.push(out);
      return out;
    }

    charge(points) {
      this.score += points;
      if (this.score < 0) this.score = 0;
      const cap = this.rules.score_cap || 0;
      if (cap > 0 && this.score > cap) this.score = cap;
    }

    setState(t, state) {
      if (!state || state === this.state) return;
      this.state = state;
      this.add({ t_ms: t, kind: EVENT.state, state });
    }

    end(t, reason) {
      if (this.ended) return;
      this.ended = true;
      this.endedMs = t;
      this.add({ t_ms: t, kind: EVENT.end, reason });
    }

    // next is the first thing that happens, with the order of a moment: a
    // follow up that ends, a timeout, an appearance, the end.
    next() {
      let when = 0;
      let what = '';
      let which = -1;
      const take = (t, kind, index) => {
        if (what === '' || t < when) {
          when = t;
          what = kind;
          which = index;
        }
      };
      if (this.playing >= 0) take(this.playUntil, 'followup', this.playing);
      this.appearances.forEach((a, i) => {
        if (this.started[i] && !this.done[i]) take(this.endAt[i], 'timeout', i);
      });
      this.appearances.forEach((a, i) => {
        if (!this.started[i]) take(this.startAt[i], 'appear', i);
      });
      if (this.durationMs > 0) take(this.durationMs, 'end', -1);
      return { when, what, which };
    }

    advance(t) {
      while (!this.ended) {
        const { when, what, which } = this.next();
        if (what === '' || when > t) {
          this.now = t;
          return;
        }
        this.now = when;
        switch (what) {
          case 'followup':
            this.finishFollowup(when, which);
            break;
          case 'timeout':
            this.timeout(when, which);
            break;
          case 'appear':
            this.appear(when, which);
            break;
          default:
            this.end(when, END.done);
            break;
        }
      }
      this.now = t;
    }

    appear(t, i) {
      const a = this.appearances[i];
      this.started[i] = true;
      this.add({ t_ms: t, kind: EVENT.appear, appearance: a.id });
      this.setState(t, a.media_state);
    }

    timeout(t, i) {
      const a = this.appearances[i];
      this.done[i] = true;
      this.timeouts++;
      const costs = !!this.rules.timeout_counts_as_hit && a.on_timeout !== 'nothing';
      if (costs) {
        this.charge(-(this.rules.timeout_penalty || 0));
        if ((this.rules.lives || 0) > 0 && this.lives > 0) this.lives--;
      }
      this.add({ t_ms: t, kind: EVENT.timeout, appearance: a.id });
      if (costs && (this.rules.lives || 0) > 0 && this.lives === 0) this.end(t, END.lives);
      else if (a.on_timeout === 'end') this.end(t, END.timeout);
    }

    // shoot is section 7.2: the first live zone in manifest order that holds
    // the point is the hit, anything else is a miss.
    shoot(t, x, y) {
      this.advance(t);
      if (this.ended) return null;
      const found = this.zoneAt(t, x, y);
      if (found.zone < 0) {
        this.misses++;
        this.charge(-(this.rules.miss_penalty || 0));
        return this.add({ t_ms: t, kind: EVENT.miss });
      }
      const z = this.zones[found.zone];
      const a = this.appearances[found.appearance];
      const points = z.points_value === null || z.points_value === undefined
        ? (this.rules.points_per_hit_default || 0)
        : z.points_value;
      this.hits++;
      this.charge(points);
      this.left[found.appearance]--;
      const event = this.add({
        t_ms: t, kind: EVENT.hit, appearance: a.id, zone: z.id, zone_class: z.zone_class, points,
      });
      if (this.left[found.appearance] > 0) return event;
      this.done[found.appearance] = true;
      this.add({ t_ms: t, kind: EVENT.clear, appearance: a.id, zone: z.id, zone_class: z.zone_class });
      this.startFollowup(t, found.appearance, z.zone_class);
      return event;
    }

    startFollowup(t, appearance, zoneClass) {
      const a = this.appearances[appearance];
      for (let i = 0; i < this.followups.length; i++) {
        const f = this.followups[i];
        if (f.appearance !== a.id || f.zone_class !== zoneClass) continue;
        this.playing = i;
        this.playUntil = t + f.duration_ms;
        this.setState(t, f.media_state);
        return;
      }
    }

    finishFollowup(t, index) {
      const f = this.followups[index];
      this.playing = -1;
      this.playUntil = 0;
      if (f.then === 'next') this.jump(t);
      else if (typeof f.then === 'string' && f.then.startsWith('back:')) this.setState(t, f.then.slice(5));
    }

    // jump moves the window of the next appearance that has not started to
    // now, keeping its length.
    jump(t) {
      let next = -1;
      this.appearances.forEach((a, i) => {
        if (this.started[i] || this.startAt[i] <= t) return;
        if (next < 0 || this.startAt[i] < this.startAt[next]) next = i;
      });
      if (next < 0) return;
      const shift = this.startAt[next] - t;
      this.startAt[next] -= shift;
      this.endAt[next] -= shift;
    }

    zoneAt(t, x, y) {
      for (let i = 0; i < this.zones.length; i++) {
        const z = this.zones[i];
        const owner = this.ownerOf(z.id);
        if (owner < 0 || !this.started[owner] || this.done[owner]) continue;
        if (t < this.startAt[owner] || t >= this.endAt[owner]) continue;
        const shape = shapeAt(z, t);
        if (inShape(z.shape, shape.points, shape.radius, x, y)) return { zone: i, appearance: owner };
      }
      return { zone: -1, appearance: -1 };
    }

    ownerOf(zone) {
      for (let i = 0; i < this.appearances.length; i++) {
        if ((this.appearances[i].zones || []).includes(zone)) return i;
      }
      return -1;
    }

    // live lists the zones that can be hit at t, for the preview to draw.
    live(t) {
      const out = [];
      this.zones.forEach((z) => {
        const owner = this.ownerOf(z.id);
        if (owner < 0 || !this.started[owner] || this.done[owner]) return;
        if (t < this.startAt[owner] || t >= this.endAt[owner]) return;
        out.push({ zone: z, shape: shapeAt(z, t) });
      });
      return out;
    }

    // trace is the run so far, in the shape the Go engine writes.
    trace() {
      return {
        events: this.events,
        score: this.score,
        hits: this.hits,
        misses: this.misses,
        timeouts: this.timeouts,
        lives: this.lives,
        ended_ms: this.endedMs,
      };
    }
  }

  // run plays a whole list of shots at once, which is what the trace
  // comparison uses.
  function run(manifest, shots) {
    const play = new Run(manifest);
    let now = 0;
    (shots || []).forEach((shot) => {
      if (shot.t_ms > now) now = shot.t_ms;
      if (play.ended) return;
      play.shoot(now, shot.x, shot.y);
    });
    if (!play.ended) play.advance(play.durationMs);
    if (!play.ended) play.end(play.durationMs, END.done);
    return play.trace();
  }

  window.cyb3rgunRules = { Run, run, shapeAt, inShape, EVENT, END };
})();
