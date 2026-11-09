// Client router of Vela App (docs/APP.md). This module may import only the
// public entry points of @govela/runtime and its own files (docs/APP.md B-4,
// TestAppLayerIsSeparable).
import { tick, writable } from '@govela/runtime';
import { hydrate, preloadHydration, replaceAppHead, serverComponent, staticHTML } from '@govela/runtime/hydrate';

// The navigation protocol version (docs/APP.md N-1).
export const protocolVersion = 1;

// Request headers of a navigation (docs/APP.md N-2).
export const headers = Object.freeze({
  navigate: 'X-Vela-Navigate',
  have: 'X-Vela-Have',
  build: 'X-Vela-Build',
  // "prefetch" on a prefetch (G-4): the answer sets no cookies.
  purpose: 'X-Vela-Purpose',
});

// A chain of redirects longer than this loads the document (N-5).
const maxRedirects = 10;
// The key of the history index in history.state.
const indexKey = 'vela:index';
// The sessionStorage key of the saved scroll positions.
const scrollKey = 'vela:scroll';

const navigatingStore = writable(null);

// navigating is a store: null, or { from, to, type } while a navigation
// loads, type 'form' while a form action runs. from and to are { url,
// route } with route the route id.
export const navigating = { subscribe: navigatingStore.subscribe };

const beforeHooks = new Set();
const navigateHooks = new Set();
const afterHooks = new Set();

// beforeNavigate(fn) calls fn({ from, to, type, cancel }) before every
// navigation of the router; cancel() keeps the current page. Full loads,
// such as leaving the site, are not reported. It returns a function that
// removes fn: return it from onMount, so it is removed on destroy.
export function beforeNavigate(fn) {
  beforeHooks.add(fn);
  return () => beforeHooks.delete(fn);
}

// onNavigate(fn) calls fn({ from, to, type }) once a navigation of the
// router has what it shows, right before the page changes (task 096). The
// change waits for a promise fn returns, so fn can start a view transition
// or an exit animation; a function fn returns (or resolves with) runs after
// the page changed. It returns a function that removes fn.
export function onNavigate(fn) {
  navigateHooks.add(fn);
  return () => navigateHooks.delete(fn);
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

// start({ VelaApp, routes, root, onMismatch, preload, viewTransitions })
// hydrates the app
// root of a server document (R-8) and installs the router: it loads the
// lazy components the payload names, adopts the server DOM, and from then
// on navigates inside the app without loading documents (N-2) and posts
// forms with fetch. root is the element or its id ('vela-app'); preload is
// the default prefetch mode of links ('hover', 'viewport' or 'off'). It
// resolves with { app, routes } and dispatches 'vela:hydrated' on root;
// every navigation of the router then dispatches 'vela:navigated', and a
// failed form action 'vela:form'. viewTransitions: true changes the page of
// a navigation inside document.startViewTransition where the browser has it
// and prefers-reduced-motion does not ask for less motion.
export async function start({ VelaApp, routes, root = 'vela-app', onMismatch, preload = 'hover', viewTransitions = false } = {}) {
  if (typeof VelaApp !== 'function') throw new TypeError('@govela/app/client: start() needs VelaApp');
  const target = typeof root === 'string' ? document.getElementById(root) : root;
  if (!target) throw new Error(`@govela/app/client: no element #${root} in the document`);
  // hydrate() removes the payload; the router keeps route and chain (N-7).
  const props = payloadProps(target);
  await preloadHydration(target);
  const options = onMismatch ? { onMismatch } : {};
  const app = hydrate(VelaApp, target, options);
  if (routes && props && Array.isArray(props.chain)) {
    router = createRouter({ VelaApp, app, routes, target, route: props.route, chain: props.chain, preload, viewTransitions });
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

function createRouter({ VelaApp, app, routes, target, route, chain, preload, viewTransitions }) {
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
    if (opts.invalidate) clearPrefetched();
    navigatingStore.set({ from, to, type: opts.type });
    // The modules of a data or client-only route (task 092) load while the
    // server answers; another route may get a fragment that needs none of
    // them.
    if (match.route.render === 'data' || match.route.render === 'client') {
      for (const level of match.route.chain) components(routes, level.id)?.load().catch(() => {});
    }

    let body;
    let at = url;
    const have = opts.invalidate ? '' : haveKeys().join(',');
    for (let redirects = 0; ; redirects++) {
      let result;
      try {
        // A prefetched answer serves one navigation (task 078).
        const cached = redirects === 0 && !opts.invalidate ? takePrefetched(at, have) : null;
        result = await (cached ? cached.catch(() => fetchNavigation(at, have, own.signal)) : fetchNavigation(at, have, own.signal));
      } catch {
        if (own.signal.aborted) return;
        // A network error or an answer that is not the protocol (N-1).
        return fullLoad(at, how);
      }
      if (controller !== own) return;
      if (result && result.redirected) {
        // Not the app's answer, such as a proxy's redirect.
        return fullLoad(new URL(result.redirected), how);
      }
      body = result;
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
    await show(body, { at, how, opts, own, from, to, fallback: () => fullLoad(at, how) });
  }

  // fetchNavigation asks the server for the navigation to url (N-2). It
  // resolves with the JSON body, or { redirected } when something other
  // than the app redirected the request.
  async function fetchNavigation(url, have, signal, purpose) {
    const requestHeaders = { [headers.navigate]: `auto;v=${protocolVersion}`, [headers.have]: have, [headers.build]: build };
    if (purpose) requestHeaders[headers.purpose] = purpose;
    const response = await fetch(url.href, {
      credentials: 'same-origin',
      signal,
      headers: requestHeaders,
    });
    if (response.redirected) return { redirected: response.url };
    return response.json();
  }

  // show puts a data or fragment response on the page: it loads what the
  // response needs, updates the history, the app root and the head, then
  // scroll and focus, and reports the navigation. fallback is the full
  // load of a response the router cannot show.
  async function show(body, { at, how, opts, own, from, to, fallback, form = false }) {
    const fragment = body.type === 'fragment' ? body.fragment : null;
    if ((body.type !== 'data' && !(fragment && typeof fragment.html === 'string')) || !Array.isArray(body.segments) || !(body.start >= 0 && body.start <= chain.length)) {
      return fallback();
    }
    let view = null;
    try {
      view = await prepare(body);
    } catch {
      if (controller === own) fallback();
      return;
    }
    if (controller !== own) return;
    // onNavigate hooks may delay the change (task 096).
    const navigation = { from, to, type: opts.type };
    const after = [];
    if (navigateHooks.size) {
      const results = await Promise.all([...navigateHooks].map((fn) => fn(navigation)));
      for (const result of results) if (typeof result === 'function') after.push(result);
      if (controller !== own) return;
    }
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
    const change = async () => {
      apply(body, view);
      applyHead(body.head);
      await tick();
    };
    if (viewTransitions && typeof document.startViewTransition === 'function' && !matchMedia('(prefers-reduced-motion: reduce)').matches) {
      await document.startViewTransition(change).updateCallbackDone;
    } else {
      await change();
    }
    for (const fn of after) fn();

    if (!opts.keep) {
      if (how === 'popstate') {
        const saved = scroll[index];
        scrollTo(saved ? saved.x : 0, saved ? saved.y : 0);
      } else if (!form) {
        scrollToHash(at);
      }
      if (!form) moveFocus(target, autofocus);
      announcer.textContent = document.title || at.pathname;
    }
    observeViewport();
    navigatingStore.set(null);
    const done = { from, to, type: opts.type };
    for (const fn of [...afterHooks]) fn(done);
    target.dispatchEvent(new CustomEvent('vela:navigated', { bubbles: true, detail: done }));
  }

  // prepare loads the modules a data response needs, or makes the
  // component value of a fragment (R-8): staticHTML, or serverComponent
  // after the lazy pages inside it loaded.
  async function prepare(body) {
    const fragment = body.type === 'fragment' ? body.fragment : null;
    if (!fragment) {
      await Promise.all(body.segments.map((segment) => components(routes, segment.id)?.load()));
      return null;
    }
    if (fragment.static) {
      const view = staticHTML(fragment.html);
      statics.add(view);
      return view;
    }
    const parsed = document.createElement('template');
    parsed.innerHTML = fragment.html;
    await preloadHydration(parsed.content);
    return adoptFragment(fragment.html, body.start);
  }

  // ── Prefetch (task 078) ────────────────────────────────────────────────
  // A link prefetches its navigation on hover (after 50 ms), on touchstart,
  // or when it enters the viewport, as data-vela-preload on it or an
  // ancestor says ("hover", "viewport" or "off"; start({ preload }) is the
  // default). The answer serves one navigation within 30 s, for the same
  // URL and X-Vela-Have; an answer marked cache: "once" (a user-dependent
  // one, G-4) never serves more. The request says X-Vela-Purpose: prefetch,
  // so the server sets no cookies on it. Every action and invalidate()
  // clear the cache: an answer prefetched before a logout is not shown.
  const prefetched = new Map();
  const prefetchTTL = 30000;

  function prefetchKey(url, have) {
    return `${withoutHash(url.href)}\n${have}`;
  }

  function takePrefetched(url, have) {
    const key = prefetchKey(url, have);
    const entry = prefetched.get(key);
    prefetched.delete(key);
    return entry && Date.now() - entry.at < prefetchTTL ? entry.body : null;
  }

  function clearPrefetched() {
    prefetched.clear();
  }

  function prefetch(url) {
    if (url.origin !== location.origin || withoutHash(url.href) === withoutHash(location.href)) return;
    const match = routes.match(url.pathname);
    if (!match) return;
    const have = haveKeys().join(',');
    const key = prefetchKey(url, have);
    const now = Date.now();
    for (const [k, entry] of prefetched) if (now - entry.at >= prefetchTTL) prefetched.delete(k);
    if (prefetched.has(key)) return;
    if (match.route.render === 'data' || match.route.render === 'client') {
      for (const level of match.route.chain) components(routes, level.id)?.load().catch(() => {});
    }
    const body = fetchNavigation(url, have, undefined, 'prefetch');
    prefetched.set(key, { at: now, body });
    // What the answer needs loads too; a failure only drops the entry.
    body.then((answer) => (answer && answer.v === protocolVersion && (answer.type === 'data' || answer.type === 'fragment') ? prepareModules(answer) : null))
      .catch(() => prefetched.delete(key));
  }

  // prepareModules loads the modules of a prefetched answer without making
  // its component value.
  function prepareModules(body) {
    if (body.type === 'data') return Promise.all(body.segments.map((segment) => components(routes, segment.id)?.load()));
    if (body.fragment && !body.fragment.static) {
      const parsed = document.createElement('template');
      parsed.innerHTML = body.fragment.html;
      return preloadHydration(parsed.content);
    }
    return null;
  }

  function preloadMode(link) {
    const holder = link.closest('[data-vela-preload]');
    return holder ? holder.getAttribute('data-vela-preload') : preload;
  }

  let hoverTimer = null;
  function onPointerOver(event) {
    const link = routableLink(event.target);
    if (!link || preloadMode(link) === 'off') return;
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(() => prefetch(new URL(link.href)), 50);
  }

  function onPointerOut(event) {
    const link = routableLink(event.target);
    if (link && !link.contains(event.relatedTarget)) clearTimeout(hoverTimer);
  }

  function onTouchStart(event) {
    const link = routableLink(event.target);
    if (link && preloadMode(link) !== 'off') prefetch(new URL(link.href));
  }

  const viewport = typeof IntersectionObserver === 'function'
    ? new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        viewport.unobserve(entry.target);
        prefetch(new URL(entry.target.href));
      }
    })
    : null;

  // observeViewport watches the links whose mode is viewport; it runs at
  // start and after each navigation.
  function observeViewport() {
    if (!viewport) return;
    viewport.disconnect();
    for (const link of document.querySelectorAll('a[href]')) {
      if (routableLink(link) === link && preloadMode(link) === 'viewport') viewport.observe(link);
    }
  }

  // ── Form actions (task 078) ────────────────────────────────────────────
  // A POST <form> inside the app root posts with fetch. A failure sets the
  // page's form prop; a success shows the data of the page with every
  // loader run again. data-vela-reload on the form or its submitter leaves
  // it to the browser.
  function onSubmit(event) {
    if (event.defaultPrevented) return;
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !target.contains(form)) return;
    const submitter = event.submitter || null;
    const attr = (name, formName) => (submitter && submitter.hasAttribute(formName) ? submitter.getAttribute(formName) : form.getAttribute(name));
    if ((attr('method', 'formmethod') || 'get').toLowerCase() !== 'post') return;
    if (form.hasAttribute('data-vela-reload') || (submitter && submitter.hasAttribute('data-vela-reload'))) return;
    const formTarget = attr('target', 'formtarget');
    if (formTarget && formTarget !== '_self') return;
    const action = new URL(submitter && submitter.hasAttribute('formaction') ? submitter.formAction : form.action, location.href);
    if (action.origin !== location.origin) return;
    event.preventDefault();
    submit(form, submitter, action, attr('enctype', 'formenctype'));
  }

  async function submit(form, submitter, action, enctype) {
    let data;
    try {
      data = new FormData(form, submitter || undefined);
    } catch {
      data = new FormData(form);
    }
    // The browser posts again by itself when the router cannot answer.
    const native = () => {
      navigatingStore.set(null);
      form.setAttribute('data-vela-reload', '');
      if (submitter && submitter.form === form) form.requestSubmit(submitter);
      else form.requestSubmit();
    };
    if (controller) controller.abort();
    const own = new AbortController();
    controller = own;
    clearPrefetched();
    const from = current();
    // The page the action belongs to: the URL without ?/name.
    const page = new URL(action.href);
    page.search = page.search.slice(1).split('&').filter((part) => part && !part.startsWith('/')).join('&');
    page.hash = '';
    const to = describe(page, routes.match(page.pathname));
    navigatingStore.set({ from, to, type: 'form' });
    let body;
    try {
      const response = await fetch(action.href, {
        method: 'POST',
        credentials: 'same-origin',
        signal: own.signal,
        body: (enctype || '').toLowerCase() === 'multipart/form-data' ? data : new URLSearchParams(data),
        headers: { [headers.navigate]: `auto;v=${protocolVersion}`, [headers.have]: '', [headers.build]: build },
      });
      if (response.redirected) {
        if (controller === own) fullLoad(new URL(response.url), 'push');
        return;
      }
      body = await response.json();
    } catch {
      if (own.signal.aborted) return;
      return native();
    }
    if (controller !== own) return;
    if (!body || body.v !== protocolVersion) return native();
    if (body.type === 'form') {
      controller = null;
      applyForm(body.form);
      await tick();
      navigatingStore.set(null);
      target.dispatchEvent(new CustomEvent('vela:form', { bubbles: true, detail: { status: body.status, form: body.form } }));
      return;
    }
    if (body.type === 'redirect') {
      controller = null;
      navigatingStore.set(null);
      return navigate(new URL(body.location, action), { type: 'goto' });
    }
    if (body.type !== 'data' && body.type !== 'fragment') return native();
    const how = withoutHash(page.href) === shown ? 'replace' : 'push';
    await show(body, { at: page, how, opts: { type: 'form', keep: how === 'replace' }, own, from, to, fallback: native, form: true });
  }

  // applyForm sets the form prop of the page level after a failed action.
  function applyForm(value) {
    const j = chain.length - 1;
    if (j < 0) return;
    apply({ start: j, segments: [{ ...chain[j], form: value }], route }, null, true);
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
  function apply(body, view, keepForm = false) {
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
    // A form prop lasts until the next navigation.
    const kept = chain.slice(0, j).map((segment) => (keepForm || !('form' in segment) ? segment : withoutForm(segment)));
    chain = kept.concat(body.segments);
    route = body.route;
    for (const entry of stack.slice()) entry.instance.set({ route, chain, start: entry.start, views: entry.views });
  }

  // routableLink is the <a> at node that the router navigates, or null.
  // An SVG <a> is left to the browser.
  function routableLink(node) {
    const link = node instanceof Element ? node.closest('a[href]') : null;
    if (!(link instanceof HTMLAnchorElement)) return null;
    if ((link.target && link.target !== '_self') || link.hasAttribute('download') || link.hasAttribute('data-vela-reload')) return null;
    if (/(^|\s)external(\s|$)/i.test(link.getAttribute('rel') || '')) return null;
    return new URL(link.href).origin === location.origin ? link : null;
  }

  function onClick(event) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const link = routableLink(event.target);
    if (!link) return;
    const url = new URL(link.href);
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
  document.addEventListener('submit', onSubmit);
  document.addEventListener('mouseover', onPointerOver);
  document.addEventListener('mouseout', onPointerOut);
  document.addEventListener('touchstart', onTouchStart, { passive: true });
  observeViewport();
  addEventListener('popstate', onPopState);
  addEventListener('pagehide', () => {
    saveScroll();
    writeScroll(scroll);
  });
  return { navigate };
}

function withoutForm(segment) {
  const { form, ...rest } = segment;
  return rest;
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

// applyHead replaces the app's title and meta tags (R-6). The tags of the
// components' <vela:head> stay: replaceAppHead lets the deeper value win
// (task 091). Without a head the page keeps its head (N-5).
function applyHead(head) {
  if (!head) return;
  const tags = [];
  if (head.title) {
    const title = document.createElement('title');
    title.textContent = head.title;
    tags.push({ node: title, depth: head.titleDepth || 0 });
  }
  for (const m of head.meta || []) {
    const meta = document.createElement('meta');
    if (m.property) meta.setAttribute('property', m.property);
    else meta.setAttribute('name', m.name || '');
    meta.setAttribute('content', m.content || '');
    tags.push({ node: meta, depth: m.depth || 0 });
  }
  replaceAppHead(tags);
}
