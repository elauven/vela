// Unit tests for runtime/runtime-blocks.js under jsdom (task 039, H1.1).
// Usage: node tools/runtime-blocks-test.mjs <dir with runtime.js and runtime-blocks.js> <jsdom module path>
import { pathToFileURL } from 'node:url';
import { join } from 'node:path';

const [runtimeDir, jsdomPath] = process.argv.slice(2);
const { JSDOM } = await import(pathToFileURL(jsdomPath).href);
const dom = new JSDOM('<!doctype html><body></body>');
for (const name of ['window', 'document', 'Node', 'HTMLElement', 'CustomEvent', 'Event']) {
  globalThis[name] = dom.window[name];
}
const rt = await import(pathToFileURL(join(runtimeDir, 'runtime-blocks.js')).href);

const failures = [];
const tests = [];
function test(name, fn) { tests.push([name, fn]); }
function assert(condition, message) { if (!condition) throw new Error(message); }
function eq(actual, expected, message) {
  if (actual !== expected) throw new Error(`${message}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const host = () => { document.body.innerHTML = ''; const el = document.createElement('div'); document.body.appendChild(el); return el; };
const text = (el) => el.textContent;
const commentCount = (el) => [...el.childNodes].filter((n) => n.nodeType === 8).length;

// A fragment that renders <tag>label</tag> and exposes its update calls.
function leaf(tag, label, log) {
  return (c, scope) => {
    const el = c.element(tag);
    c.enter(el);
    const t = c.text(typeof label === 'function' ? label(scope) : label);
    c.exit();
    return {
      p(dirty, scopeChanged) {
        if (log) log.push([dirty, scopeChanged]);
        if (typeof label === 'function') t.data = label(scope);
      },
    };
  };
}

test('owner releases cleanups in reverse order once', () => {
  const order = [];
  const owner = rt.$$owner(null);
  owner.add(() => order.push(1));
  owner.add(() => order.push(2));
  owner.release();
  owner.release();
  eq(order.join(','), '2,1', 'release order');
  owner.add(() => order.push(3));
  eq(order.join(','), '2,1,3', 'late cleanup runs immediately');
});

test('$$if switches branches and forwards updates', () => {
  const el = host();
  let which = 0;
  const log = [];
  let block;
  const root = rt.$$mountRoot(el, (c) => {
    block = c.block(rt.$$if, () => which, [leaf('b', 'zero', log), leaf('i', 'one')], (d) => d === 'cond');
    return { p: (d) => block.p(d) };
  });
  eq(el.innerHTML, '<b>zero</b><!---->', 'initial branch with one anchor');
  root.p('other');
  eq(log.length, 1, 'unrelated update forwarded to active branch');
  which = 1;
  root.p('other');
  eq(text(el), 'zero', 'branch change ignored when changed() is false');
  root.p('cond');
  eq(el.innerHTML, '<i>one</i><!---->', 'branch switched');
  which = -1;
  root.p('cond');
  eq(el.innerHTML, '<!---->', 'no branch');
});

test('$$if releases nested resources on branch switch', () => {
  const el = host();
  let show = 0;
  let clicks = 0;
  const root = rt.$$mountRoot(el, (c) => {
    const block = c.block(rt.$$if, () => show, [(c) => {
      const button = c.element('button');
      c.listen(button, 'click', () => clicks++);
      c.block(rt.$$each, () => [1, 2], null, leaf('span', (s) => String(s.item)));
    }]);
    return { p: (d) => block.p(d) };
  });
  const button = el.querySelector('button');
  button.click();
  eq(clicks, 1, 'listener attached');
  eq(text(el), '12', 'nested each rendered');
  show = -1;
  root.p();
  button.click();
  eq(clicks, 1, 'listener removed with branch');
  eq(el.querySelectorAll('span').length, 0, 'nested rows removed');
});

test('$$each keyed keeps identity, reorders, removes and keeps focus', () => {
  const el = host();
  let items = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];
  const rows = (c, s) => {
    const input = c.element('input');
    c.init(() => { input.value = s.item.id; });
    return { p() { input.value = s.item.id; } };
  };
  const root = rt.$$mountRoot(el, (c) => {
    const block = c.block(rt.$$each, () => items, (item) => item.id, rows);
    return { p: (d) => block.p(d) };
  });
  eq(commentCount(el), 1, 'single-element rows add no range comments');
  const [a, b, c] = el.querySelectorAll('input');
  b.focus();
  items = [{ id: 'c' }, { id: 'b' }, { id: 'd' }];
  root.p();
  const after = [...el.querySelectorAll('input')];
  eq(after.map((i) => i.value).join(''), 'cbd', 'order');
  assert(after[0] === c && after[1] === b, 'retained rows keep node identity');
  assert(!a.isConnected, 'removed row detached');
  eq(document.activeElement, b, 'focus kept on moved row');
});

test('$$each unkeyed updates in place with scopeChanged and shows fallback', () => {
  const el = host();
  let items = ['x', 'y'];
  const log = [];
  const root = rt.$$mountRoot(el, (c) => {
    const block = c.block(rt.$$each, () => items, null, leaf('li', (s) => s.item, log), leaf('p', 'empty'));
    return { p: (d) => block.p(d) };
  });
  const first = el.querySelector('li');
  items = ['z'];
  root.p('d');
  eq(text(el), 'z', 'updated and trimmed');
  assert(el.querySelector('li') === first, 'first row reused');
  eq(log[0][1], true, 'scopeChanged passed to row');
  items = [];
  root.p('d');
  eq(el.innerHTML, '<p>empty</p><!---->', 'fallback shown');
  items = ['q'];
  root.p('d');
  eq(el.innerHTML, '<li>q</li><!---->', 'fallback removed');
});

test('multi-node rows are wrapped in a comment pair and move as a unit', () => {
  const el = host();
  let items = [1, 2];
  const root = rt.$$mountRoot(el, (c) => {
    const block = c.block(rt.$$each, () => items, (i) => i, (c, s) => {
      c.element('dt').textContent = 't' + s.item;
      c.element('dd').textContent = 'd' + s.item;
    });
    return { p: (d) => block.p(d) };
  });
  eq(text(el), 't1d1t2d2', 'initial');
  items = [2, 1];
  root.p();
  eq(text(el), 't2d2t1d1', 'pairs moved together');
});

test('$$key recreates its body only when the value changes', () => {
  const el = host();
  let key = 1;
  const root = rt.$$mountRoot(el, (c) => {
    const block = c.block(rt.$$key, () => key, leaf('b', 'body'));
    return { p: (d) => block.p(d) };
  });
  const first = el.querySelector('b');
  root.p();
  assert(el.querySelector('b') === first, 'same key keeps the node');
  key = 2;
  root.p();
  assert(el.querySelector('b') !== first && el.querySelector('b'), 'new key recreates the body');
});

test('$$await renders pending, then, catch and ignores stale promises', async () => {
  const el = host();
  let resolveFirst;
  let promise = new Promise((resolve) => { resolveFirst = resolve; });
  const root = rt.$$mountRoot(el, (c) => {
    const block = c.block(rt.$$await, () => promise, leaf('i', 'wait'), leaf('u', (s) => 'ok ' + s.value), leaf('s', (s) => 'err ' + s.error));
    return { p: (d) => block.p(d) };
  });
  eq(text(el), 'wait', 'pending');
  const stale = resolveFirst;
  promise = Promise.reject('boom');
  root.p();
  stale('late');
  await tick();
  eq(text(el), 'err boom', 'catch branch, stale value ignored');
  promise = 42;
  root.p();
  await tick();
  eq(text(el), 'ok 42', 'non-promise value settles as then');
});

test('$$await does not render after release', async () => {
  const el = host();
  let resolve;
  const root = rt.$$mountRoot(el, (c) => {
    c.block(rt.$$await, () => new Promise((r) => { resolve = r; }), null, leaf('u', 'done'));
  });
  root.release();
  resolve(1);
  await tick();
  eq(el.querySelectorAll('u').length, 0, 'settlement after release is inert');
});

test('$$component sets props on change, forwards events and destroys', () => {
  const el = host();
  const calls = [];
  const factory = (anchor, props) => {
    const root = document.createElement('section');
    root.textContent = props.title;
    anchor.parentNode.insertBefore(root, anchor);
    calls.push('mount');
    return {
      el: root,
      set(next) { calls.push('set'); root.textContent = next.title; },
      destroy() { calls.push('destroy'); root.remove(); },
    };
  };
  let title = 'a';
  let events = 0;
  const root = rt.$$mountRoot(el, (c) => {
    const block = c.block(rt.$$component, factory, () => ({ title }), {}, { close: () => events++ }, (d) => d === 'title');
    return { p: (d) => block.p(d) };
  });
  title = 'b';
  root.p('other');
  eq(text(el), 'a', 'no set for unrelated change');
  root.p('title');
  eq(text(el), 'b', 'props set');
  el.querySelector('section').dispatchEvent(new CustomEvent('close'));
  eq(events, 1, 'component event forwarded');
  root.release();
  el.querySelector('section') && el.querySelector('section').dispatchEvent(new CustomEvent('close'));
  eq(calls.join(','), 'mount,set,destroy', 'lifecycle');
});

test('$$slot mounts parent content or fallback; content updates come from the parent', () => {
  const el = host();
  const live = new Set();
  let label = 'one';
  const content = rt.$$slotContent(live, leaf('em', () => label));
  const root = rt.$$mountRoot(el, (c) => {
    c.block(rt.$$slot, content, leaf('small', 'fallback'));
    c.block(rt.$$slot, undefined, leaf('small', 'fallback'));
  });
  eq(text(el), 'onefallback', 'content and fallback');
  eq(live.size, 1, 'content registered with the parent');
  label = 'two';
  for (const handle of live) handle.p();
  eq(text(el), 'twofallback', 'parent update reached the content');
  root.release();
  eq(live.size, 0, 'content unregistered on release');
});

test('$$client mounts the main branch in CSR', () => {
  const el = host();
  rt.$$mountRoot(el, (c) => { c.block(rt.$$client, leaf('canvas', ''), leaf('p', 'fallback')); });
  eq(el.querySelectorAll('canvas').length, 1, 'main branch mounted');
  eq(el.querySelectorAll('p').length, 0, 'fallback ignored');
});

test('abandon releases resources and leaves the DOM in place', () => {
  const el = host();
  let clicks = 0;
  let block;
  rt.$$mountRoot(el, (c) => {
    block = c.block(rt.$$if, () => 0, [(c) => {
      const button = c.element('button');
      c.listen(button, 'click', () => clicks++);
    }]);
  });
  block.abandon();
  el.querySelector('button').click();
  eq(clicks, 0, 'listener released');
  eq(el.querySelectorAll('button').length, 1, 'DOM untouched');
  block.p();
  eq(el.querySelectorAll('button').length, 1, 'abandoned block ignores updates');
});

test('static subtrees clone a module-level template', () => {
  const el = host();
  const template = rt.$$template('<p>a</p><p>b</p>');
  rt.$$mountRoot(el, (c) => { c.static(template); c.static(template); });
  eq(el.querySelectorAll('p').length, 4, 'two clones');
  eq(template.content.childNodes.length, 2, 'template untouched');
});

for (const [name, fn] of tests) {
  try {
    await fn();
  } catch (error) {
    failures.push(`${name}: ${error.message}`);
  }
}
console.log(JSON.stringify({ ok: failures.length === 0, total: tests.length, failures }));
