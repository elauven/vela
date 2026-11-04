// Client router of Vela App (docs/APP.md). This module may import only the
// public entry points of @govela/runtime and its own files (docs/APP.md B-4,
// TestAppLayerIsSeparable).
import { tick, writable } from '@govela/runtime';
import { hydrate, preloadHydration, serverComponent, staticHTML } from '@govela/runtime/hydrate';

// The navigation protocol version (docs/APP.md N-1).
export const protocolVersion = 1;

// Request headers of a navigation (docs/APP.md N-2).
export const headers = Object.freeze({
  navigate: 'X-Vela-Navigate',
  have: 'X-Vela-Have',
  build: 'X-Vela-Build',
});

// A chain of redirects longer than this loads the document (N-5).
const maxRedirects = 10;
// The key of the history index in history.state.
const indexKey = 'vela:index';
// The sessionStorage key of the saved scroll positions.
const scrollKey = 'vela:scroll';

const navigatingStore = writable(null);

// navigating is a store: null, or { from, to, type } while a navigation
// loads. from and to are { url, route } with route the route id.
export const navigating = { subscribe: navigatingStore.subscribe };

const beforeHooks = new Set();
const afterHooks = new Set();

// beforeNavigate(fn) calls fn({ from, to, type, cancel }) before every
// navigation of the router; cancel() keeps the current page. Full loads,
// such as leaving the site, are not reported. It returns a function that
// removes fn: return it from onMount, so it is removed on destroy.
export function beforeNavigate(fn) {
  beforeHooks.add(fn);
  return () => beforeHooks.delete(fn);
}

// afterNavigate(fn) calls fn({ from, to, type }) after every navigation of
// the router, once the page shows the new route. It returns a function that
// removes fn.
export function afterNavigate(fn) {
  afterHooks.add(fn);
  return () => afterHooks.delete(fn);
}

let router = null;

// goto(url, { replace, state, invalidate }) navigates like a link click.
// replace replaces the history entry; state is kept in history.state.state;
// invalidate runs every loader again. It resolves when the navigation ends.
// Before start(), and for another origin, it loads the document.
export function goto(url, { replace = false, state, invalidate = false } = {}) {
  const target = new URL(url, location.href);
  if (!router) {
    location.assign(target.href);
    return Promise.resolve();
  }
  return router.navigate(target, { type: 'goto', replace, state, invalidate });
}

// invalidate() loads the current URL again with every loader, keeping the
// scroll position, the focus and the history entry.
export function invalidate() {
  if (!router) return Promise.resolve();
  return router.navigate(new URL(location.href), { type: 'invalidate', replace: true, invalidate: true, keep: true });
}

// start({ VelaApp, routes, root, onMismatch }) hydrates the app root of a
// server document (R-8) and installs the router: it loads the lazy
// components the payload names, adopts the server DOM, and from then on
// navigates inside the app without loading documents (N-2). root is the
// element or its id ('vela-app'). It resolves with { app, routes } and
// dispatches 'vela:hydrated' on root; every navigation of the router then
// dispatches 'vela:navigated'.
export async function start({ VelaApp, routes, root = 'vela-app', onMismatch } = {}) {
  if (typeof VelaApp !== 'function') throw new TypeError('@govela/app/client: start() needs VelaApp');
  const target = typeof root === 'string' ? document.getElementById(root) : root;
  if (!target) throw new Error(`@govela/app/client: no element #${root} in the document`);
  // hydrate() removes the payload; the router keeps route and chain (N-7).
  const props = payloadProps(target);
  await preloadHydration(target);
  const options = onMismatch ? { onMismatch } : {};
  const app = hydrate(VelaApp, target, options);
  if (routes && props && Array.isArray(props.chain)) {
    router = createRouter({ VelaApp, app, routes, target, route: props.route, chain: props.chain });
  }
  target.dispatchEvent(new CustomEvent('vela:hydrated', { bubbles: true, detail: { app } }));
  return { app, routes };
}

function payloadProps(target) {
  for (const node of target.childNodes) {
    if (node.nodeType === 1 && node.localName === 'script' && node.hasAttribute('data-vela-root')) {
      try {
        return JSON.parse(node.textContent).props || null;
      } catch {
        return null;
      }
    }
  }
  return null;
}

function createRouter({ VelaApp, app, routes, target, route, chain }) {
  const build = document.querySelector('meta[name="vela-build"]')?.getAttribute('content') || '';
  const scroll = readScroll();
  let index = typeof history.state?.[indexKey] === 'number' ? history.state[indexKey] : 0;
  let controller = null;
  let ignorePop = false;
  // The VelaApp instances that render the chain (task 077): the hydrated
  // root, then one per interactive fragment, which renders the levels from
  // its start. views are the values that replace a level's component: an
  // interactive fragment (the next instance) or a static one.
  const stack = [{ instance: app, start: 0, views: {} }];
  const statics = new WeakSet();
  // The URL the page shows, without its hash.
  let shown = withoutHash(location.href);

  history.replaceState({ ...history.state, [indexKey]: index }, '');
  history.scrollRestoration = 'manual';
  if (scroll[index]) scrollTo(scroll[index].x, scroll[index].y);
  const announcer = createAnnouncer();

  const describe = (url, match) => ({ url, route: match ? match.route.id : null });
  const current = () => describe(new URL(location.href), route ? { route } : null);

  function saveScroll() {
    scroll[index] = { x: scrollX, y: scrollY };
  }

  function fullLoad(url, how) {
    navigatingStore.set(null);
    if (how === 'popstate') location.reload();
    else if (how === 'replace') location.replace(url.href);
    else location.assign(url.href);
  }

  async function navigate(url, opts) {
    const how = opts.type === 'popstate' ? 'popstate' : opts.replace ? 'replace' : 'push';
    if (url.origin !== location.origin) return fullLoad(url, how);
    const match = routes.match(url.pathname);
    const from = current();
    const to = describe(url, match);
    let cancelled = false;
    for (const fn of [...beforeHooks]) {
      fn({ from, to, type: opts.type, cancel: () => { cancelled = true; } });
    }
    if (cancelled) {
      if (opts.type === 'popstate' && opts.delta) {
        // Put the history back where the page is.
        ignorePop = true;
        history.go(-opts.delta);
      }
      return;
    }
    // A URL the client cannot route is a document (N-2).
    if (!match) return fullLoad(url, how);

    if (controller) controller.abort();
    const own = new AbortController();
    controller = own;
    navigatingStore.set({ from, to, type: opts.type });
    // The modules of a data route load while the server answers; another
    // route may get a fragment that needs none of them.
    if (match.route.render === 'data') {
      for (const level of match.route.chain) components(routes, level.id)?.load().catch(() => {});
    }

    let body;
    let at = url;
    for (let redirects = 0; ; redirects++) {
      let response;
      try {
        response = await fetch(at.href, {
          credentials: 'same-origin',
          signal: own.signal,
          headers: {
            [headers.navigate]: `auto;v=${protocolVersion}`,
            [headers.have]: opts.invalidate ? '' : haveKeys().join(','),
            [headers.build]: build,
          },
        });
        if (response.redirected) {
          // Not the app's answer, such as a proxy's redirect.
          if (controller === own) fullLoad(new URL(response.url), how);
          return;
        }
        body = await response.json();
      } catch (error) {
        if (own.signal.aborted) return;
        // A network error or an answer that is not the protocol (N-1).
        return fullLoad(at, how);
      }
      if (controller !== own) return;
      if (!body || body.v !== protocolVersion) return fullLoad(at, how);
      if (body.type !== 'redirect') break;
      const next = new URL(body.location, at);
      if (redirects + 1 >= maxRedirects || next.origin !== location.origin || !routes.match(next.pathname)) {
        return fullLoad(next, how);
      }
      at = next;
      to.url = next;
      to.route = routes.match(next.pathname).route.id;
    }
    const fragment = body.type === 'fragment' ? body.fragment : null;
    if ((body.type !== 'data' && !(fragment && typeof fragment.html === 'string')) || !Array.isArray(body.segments) || !(body.start >= 0 && body.start <= chain.length)) {
      return fullLoad(at, how);
    }
    let view = null;
    try {
      if (!fragment) {
        await Promise.all(body.segments.map((segment) => components(routes, segment.id)?.load()));
      } else if (fragment.static) {
        view = staticHTML(fragment.html);
        statics.add(view);
      } else {
        // The lazy pages inside the fragment load before it hydrates.
        const parsed = document.createElement('template');
        parsed.innerHTML = fragment.html;
        await preloadHydration(parsed.content);
        view = adoptFragment(fragment.html, body.start);
      }
    } catch {
      if (controller === own) fullLoad(at, how);
      return;
    }
    if (controller !== own) return;
    controller = null;

    if (!opts.keep) saveScroll();
    if (how === 'popstate') {
      index = opts.index;
    } else {
      const state = { [indexKey]: how === 'push' ? index + 1 : index };
      if (opts.state !== undefined) state.state = opts.state;
      if (how === 'push') {
        history.pushState(state, '', at.href);
        index++;
        // The entries after this one are gone.
        for (const key of Object.keys(scroll)) if (Number(key) > index) delete scroll[key];
      } else {
        history.replaceState(state, '', at.href);
      }
    }
    shown = withoutHash(at.href);

    const autofocus = new Set(target.querySelectorAll('[autofocus]'));
    apply(body, view);
    applyHead(body.head);
    await tick();

    if (!opts.keep) {
      if (how === 'popstate') {
        const saved = scroll[index];
        scrollTo(saved ? saved.x : 0, saved ? saved.y : 0);
      } else {
        scrollToHash(at);
      }
      moveFocus(target, autofocus);
      announcer.textContent = document.title || at.pathname;
    }
    navigatingStore.set(null);
    const done = { from, to, type: opts.type };
    for (const fn of [...afterHooks]) fn(done);
    target.dispatchEvent(new CustomEvent('vela:navigated', { bubbles: true, detail: done }));
  }

  // haveKeys are the keys of X-Vela-Have: the chain up to the first level
  // of a static fragment, whose levels hold no data and render as one
  // piece, so the server sends them again when anything in them changes.
  function haveKeys() {
    let limit = chain.length;
    for (const entry of stack) {
      for (const [level, value] of Object.entries(entry.views)) {
        if (statics.has(value)) limit = Math.min(limit, Number(level));
      }
    }
    return chain.slice(0, limit).map((segment) => segment.key);
  }

  // adoptFragment is the component value of an interactive fragment: a
  // VelaApp root rendered from level start. The instance it creates joins
  // the stack when it mounts.
  function adoptFragment(html, level) {
    const adopted = serverComponent(VelaApp, html);
    const view = function (mountTarget, props, slots) {
      const instance = adopted(mountTarget, props, slots);
      stack.push({ instance, start: level, views: {} });
      return instance;
    };
    // The wrapper keeps what the runtime reads from the factory, such as
    // its declared props.
    return Object.assign(view, adopted);
  }

  // apply puts a data or fragment response on the page (N-7, R-8). Data
  // from level j goes to the innermost instance that renders level j; a
  // fragment at j goes into the views of the innermost instance that holds
  // level j below its own first level (the root holds every level). The
  // instances below the target go with the levels they rendered.
  function apply(body, view) {
    const j = body.start;
    let t = stack.length - 1;
    while (t > 0 && (view ? stack[t].start >= j : stack[t].start > j)) t--;
    stack.length = t + 1;
    const owner = stack[t];
    const views = {};
    for (const [level, value] of Object.entries(owner.views)) {
      if (Number(level) < j) views[level] = value;
    }
    if (view) views[j] = view;
    owner.views = views;
    chain = chain.slice(0, j).concat(body.segments);
    route = body.route;
    for (const entry of stack.slice()) entry.instance.set({ route, chain, start: entry.start, views: entry.views });
  }

  function onClick(event) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const link = event.target instanceof Element ? event.target.closest('a[href]') : null;
    // An SVG <a> is left to the browser.
    if (!(link instanceof HTMLAnchorElement)) return;
    if ((link.target && link.target !== '_self') || link.hasAttribute('download') || link.hasAttribute('data-vela-reload')) return;
    if (/(^|\s)external(\s|$)/i.test(link.getAttribute('rel') || '')) return;
    const url = new URL(link.href);
    if (url.origin !== location.origin) return;
    // A #hash on the same page scrolls; the browser does that.
    if (url.hash && withoutHash(url.href) === withoutHash(location.href)) return;
    event.preventDefault();
    navigate(url, { type: 'link', replace: url.href === location.href });
  }

  function onPopState(event) {
    const to = event.state && typeof event.state[indexKey] === 'number' ? event.state[indexKey] : null;
    if (ignorePop) {
      ignorePop = false;
      if (to !== null) index = to;
      return;
    }
    if (to === null) {
      // An entry the browser made for a #hash link: give it an index.
      saveScroll();
      index++;
      history.replaceState({ ...event.state, [indexKey]: index }, '');
      if (withoutHash(location.href) === shown) return;
      navigate(new URL(location.href), { type: 'popstate', index, delta: 0 });
      return;
    }
    if (withoutHash(location.href) === shown) {
      // Only the hash differs.
      saveScroll();
      index = to;
      return;
    }
    navigate(new URL(location.href), { type: 'popstate', index: to, delta: to - index });
  }

  document.addEventListener('click', onClick);
  addEventListener('popstate', onPopState);
  addEventListener('pagehide', () => {
    saveScroll();
    writeScroll(scroll);
  });
  return { navigate };
}

function components(routes, id) {
  return routes.components ? routes.components[id] || null : null;
}

function withoutHash(href) {
  const at = href.indexOf('#');
  return at < 0 ? href : href.slice(0, at);
}

function readScroll() {
  try {
    return JSON.parse(sessionStorage.getItem(scrollKey)) || {};
  } catch {
    return {};
  }
}

function writeScroll(scroll) {
  try {
    sessionStorage.setItem(scrollKey, JSON.stringify(scroll));
  } catch {
    // Storage may be unavailable; positions are then kept for this page only.
  }
}

function scrollToHash(url) {
  if (url.hash) {
    let id = url.hash.slice(1);
    try {
      id = decodeURIComponent(id);
    } catch {
      // Keep the raw hash.
    }
    const element = document.getElementById(id) || document.getElementsByName(id)[0];
    if (element) {
      element.scrollIntoView();
      return;
    }
  }
  scrollTo(0, 0);
}

// moveFocus focuses the first [autofocus] element the navigation added, or
// the body, so that keyboard and screen reader users start on the new page.
function moveFocus(target, before) {
  const added = [...target.querySelectorAll('[autofocus]')].find((element) => !before.has(element));
  if (added && typeof added.focus === 'function') {
    added.focus();
    return;
  }
  const body = document.body;
  const had = body.hasAttribute('tabindex');
  if (!had) body.setAttribute('tabindex', '-1');
  body.focus({ preventScroll: true });
  if (!had) body.removeAttribute('tabindex');
}

// createAnnouncer adds a polite live region that announces the title of
// each new page; it is outside the app root, so hydration never sees it.
function createAnnouncer() {
  const region = document.createElement('div');
  region.id = 'vela-announcer';
  region.setAttribute('aria-live', 'polite');
  region.setAttribute('aria-atomic', 'true');
  region.style.cssText = 'position:absolute;left:0;top:0;width:1px;height:1px;padding:0;overflow:hidden;clip:rect(0 0 0 0);clip-path:inset(50%);white-space:nowrap;border:0';
  document.body.appendChild(region);
  return region;
}

// applyHead replaces the title and the <meta data-vela-head> tags (R-6).
// Without a head the page keeps its head (N-5).
function applyHead(head) {
  if (!head) return;
  document.title = head.title || '';
  for (const meta of document.head.querySelectorAll('meta[data-vela-head]')) meta.remove();
  for (const m of head.meta || []) {
    const meta = document.createElement('meta');
    if (m.property) meta.setAttribute('property', m.property);
    else meta.setAttribute('name', m.name || '');
    meta.setAttribute('content', m.content || '');
    meta.setAttribute('data-vela-head', '');
    document.head.appendChild(meta);
  }
}
