// preview.js plays a draft in the browser with the mouse as the pistol
// (D-044): the clock, the clips, the score panel and the immediate
// reactions. The rules themselves are in rules.js, which is the same engine
// the Go side runs; the check button plays the shots of this run through
// both and says whether they agree.
(() => {
  const root = document.getElementById('preview');
  if (!root || !window.cyb3rgunRules) return;

  const urls = root.dataset;
  const words = JSON.parse(root.dataset.words || '{}');
  const tier = root.dataset.tier || 'video';

  const video = document.getElementById('preview-video');
  const overlay = document.getElementById('preview-overlay');
  const canvas = document.getElementById('preview-canvas');
  const ctx = canvas.getContext('2d');
  const sound = document.getElementById('preview-sound');
  const result = document.getElementById('preview-result');

  const state = {
    manifest: null,
    engine: null,
    running: false,
    startedAt: 0,
    now: 0,
    shots: [],
    marks: [], // the hit marks still on the screen
    seen: 0, // events already handled
    clip: '',
  };

  function media() {
    return (state.manifest && state.manifest.media) || {};
  }

  function immediate() {
    return ((state.manifest && state.manifest.reaction) || {}).immediate || {};
  }

  function canvasSize() {
    const display = (state.manifest && state.manifest.display) || {};
    const size = display.canvas || {};
    return { w: size.w || 1080, h: size.h || 1920 };
  }

  function fileOf(path) {
    const cut = path.lastIndexOf('/');
    return urls.file + encodeURIComponent(cut < 0 ? path : path.slice(cut + 1));
  }

  async function load() {
    const answer = await fetch(urls.draft, { headers: { Accept: 'application/json' } });
    if (!answer.ok) return;
    const draft = await answer.json();
    state.manifest = typeof draft.manifest === 'string' ? JSON.parse(draft.manifest) : draft.manifest;
    const size = canvasSize();
    canvas.width = size.w;
    canvas.height = size.h;
    restart();
  }

  function restart() {
    state.engine = new window.cyb3rgunRules.Run(state.manifest);
    state.shots = [];
    state.marks = [];
    state.seen = 0;
    state.now = 0;
    state.clip = '';
    state.running = false;
    result.textContent = '';
    setClip(baseClip(), 0);
    showScore();
    draw();
  }

  // baseClip is what plays before anything happens: the main video, or the
  // first media state of an interactive scenario.
  function baseClip() {
    if (tier !== 'interactive') return media().main || '';
    const states = media().state || {};
    const names = Object.keys(states).sort();
    return names.length > 0 ? states[names[0]] : '';
  }

  function setClip(path, at) {
    if (!path) return;
    if (path !== state.clip) {
      state.clip = path;
      video.src = fileOf(path);
    }
    const seconds = at / 1000;
    const seek = () => {
      if (isFinite(video.duration) && seconds <= video.duration) video.currentTime = seconds;
      else video.currentTime = 0;
    };
    if (video.readyState > 0) seek();
    else video.addEventListener('loadedmetadata', seek, { once: true });
  }

  function start() {
    if (state.running || !state.engine || state.engine.ended) return;
    state.running = true;
    state.startedAt = performance.now() - state.now;
    video.play().catch(() => {});
    requestAnimationFrame(tick);
  }

  function tick(stamp) {
    if (!state.running) return;
    const now = Math.round(stamp - state.startedAt);
    state.now = now;
    state.engine.advance(now);
    handleEvents();
    if (state.engine.ended) {
      state.running = false;
      video.pause();
      result.textContent = words.ended || '';
    }
    showScore();
    draw();
    if (state.running) requestAnimationFrame(tick);
  }

  // handleEvents reacts to what the engine decided since the last frame: a
  // state change switches the clip, an end stops the run.
  function handleEvents() {
    const events = state.engine.events;
    for (; state.seen < events.length; state.seen++) {
      const event = events[state.seen];
      if (event.kind !== window.cyb3rgunRules.EVENT.state) continue;
      const path = (media().state || {})[event.state];
      if (!path) continue;
      // A follow up plays from its start; a state of an appearance runs on
      // the scenario clock.
      setClip(path, state.engine.playing >= 0 ? 0 : state.now);
      video.play().catch(() => {});
    }
  }

  function showScore() {
    const engine = state.engine;
    const set = (id, value) => {
      const box = document.getElementById(id);
      if (box) box.textContent = String(value);
    };
    set('score-points', engine.score);
    set('score-hits', engine.hits);
    set('score-misses', engine.misses);
    set('score-timeouts', engine.timeouts);
    set('score-lives', engine.lives);
    set('score-time', (state.now / 1000).toFixed(1));
  }

  function at(event) {
    const box = canvas.getBoundingClientRect();
    const size = canvasSize();
    return {
      x: Math.round(((event.clientX - box.left) / box.width) * size.w),
      y: Math.round(((event.clientY - box.top) / box.height) * size.h),
    };
  }

  canvas.addEventListener('pointerdown', (event) => {
    if (!state.engine) return;
    if (!state.running) {
      start();
      return;
    }
    const point = at(event);
    const t = state.now;
    const decided = state.engine.shoot(t, point.x, point.y);
    state.shots.push({ t_ms: t, x: point.x, y: point.y });
    handleEvents();
    if (decided) {
      state.marks.push({ x: point.x, y: point.y, until: t + 900, hit: decided.kind === 'hit' });
      if (decided.kind === 'hit') fire();
    }
    showScore();
    draw();
  });

  // fire is the immediate reaction of section 5: the flash, the sound and
  // the overlay clip, all in the frame of the hit.
  function fire() {
    const now = immediate();
    if (now.impact_sound) {
      sound.src = fileOf(now.impact_sound);
      sound.currentTime = 0;
      sound.play().catch(() => {});
    }
    if (now.blood) {
      overlay.src = fileOf(now.blood);
      overlay.hidden = false;
      overlay.currentTime = 0;
      overlay.play().catch(() => {});
      overlay.addEventListener('ended', () => {
        overlay.hidden = true;
      }, { once: true });
    }
  }

  function draw() {
    const size = canvasSize();
    ctx.clearRect(0, 0, size.w, size.h);
    if (!state.engine) return;

    // The zones that can be hit right now, thin, so a person can see what
    // the rules see.
    state.engine.live(state.now).forEach((item) => {
      const shape = item.shape;
      const points = shape.points || [];
      if (points.length === 0) return;
      ctx.lineWidth = 3;
      ctx.strokeStyle = 'rgba(49, 208, 122, 0.55)';
      ctx.beginPath();
      if (item.zone.shape === 'circle') {
        ctx.arc(points[0][0], points[0][1], Math.max(1, shape.radius || 0), 0, Math.PI * 2);
      } else if (item.zone.shape === 'rect' && points.length > 1) {
        ctx.rect(points[0][0], points[0][1], points[1][0] - points[0][0], points[1][1] - points[0][1]);
      } else {
        ctx.moveTo(points[0][0], points[0][1]);
        points.slice(1).forEach((p) => ctx.lineTo(p[0], p[1]));
        ctx.closePath();
      }
      ctx.stroke();
    });

    // The marks of the last shots, and the flash of a fresh hit.
    const marker = immediate().hitmarker || 'none';
    state.marks = state.marks.filter((mark) => mark.until > state.now);
    let flash = false;
    state.marks.forEach((mark) => {
      flash = flash || (mark.hit && mark.until - state.now > 830);
      ctx.lineWidth = 6;
      ctx.strokeStyle = mark.hit ? '#ffb000' : 'rgba(255,255,255,0.7)';
      if (marker === 'ring' || !mark.hit) {
        ctx.beginPath();
        ctx.arc(mark.x, mark.y, 26, 0, Math.PI * 2);
        ctx.stroke();
      }
      if (marker === 'cross' && mark.hit) {
        ctx.beginPath();
        ctx.moveTo(mark.x - 26, mark.y - 26);
        ctx.lineTo(mark.x + 26, mark.y + 26);
        ctx.moveTo(mark.x + 26, mark.y - 26);
        ctx.lineTo(mark.x - 26, mark.y + 26);
        ctx.stroke();
      }
    });
    if (flash && immediate().flash) {
      ctx.fillStyle = 'rgba(255,255,255,0.35)';
      ctx.fillRect(0, 0, size.w, size.h);
    }
  }

  // check plays the shots of this run through the Go engine as well and
  // says whether the two agree (D-044).
  async function check() {
    if (!state.manifest) return;
    const mine = window.cyb3rgunRules.run(state.manifest, state.shots);
    const answer = await fetch(urls.trace, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ shots: state.shots }),
    });
    if (!answer.ok) {
      result.textContent = words.failed || '';
      result.className = 'failed';
      return;
    }
    const data = await answer.json();
    const same = JSON.stringify(theirs(data.trace)) === JSON.stringify(theirs(mine));
    result.textContent = same ? words.agree : words.differ;
    result.className = same ? 'ok' : 'failed';
    window.cyb3rgunPreview = { mine, theirs: data.trace, same, shots: state.shots };
  }

  // theirs puts a trace into one shape, so that a missing empty list and an
  // empty list are the same thing.
  function theirs(trace) {
    return {
      events: (trace.events || []).map((e) => ({
        t_ms: e.t_ms, kind: e.kind, appearance: e.appearance || '', zone: e.zone || '',
        zone_class: e.zone_class || '', state: e.state || '', reason: e.reason || '',
        points: e.points || 0, score: e.score, lives: e.lives,
      })),
      score: trace.score, hits: trace.hits, misses: trace.misses,
      timeouts: trace.timeouts, lives: trace.lives, ended_ms: trace.ended_ms,
    };
  }

  document.addEventListener('click', (event) => {
    switch (event.target.id) {
      case 'preview-start':
        start();
        break;
      case 'preview-restart':
        restart();
        break;
      case 'preview-check':
        check();
        break;
      default:
        break;
    }
  });

  document.addEventListener('keydown', (event) => {
    if (event.key !== ' ' || event.target.tagName === 'BUTTON') return;
    event.preventDefault();
    if (state.running) {
      state.running = false;
      video.pause();
    } else {
      start();
    }
  });

  // The browser proof reads this to compare the two engines.
  window.cyb3rgunPlay = state;

  load();
})();
