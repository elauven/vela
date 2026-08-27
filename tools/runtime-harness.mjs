// runtime-harness.mjs — behavioural test driver for compiled Vela
// components (E-stabilize 1).
//
// The Go test (runtime_test.go) compiles a .vela source, writes the
// generated JS, and invokes this script with:
//
//   node tools/runtime-harness.mjs <component.js> <export-name> <steps.json>
//
// steps.json is an array of step objects describing actions to take
// and DOM assertions to check.  The script mounts the component in a
// jsdom document, replays the steps, and prints a JSON result of the
// form { ok: bool, failures: [string] } on stdout.
//
// Step kinds:
//   { "assertText":   "<sel>", "equals": "<str>" }   textContent of first match
//   { "assertHTML":   "<sel>", "contains": "<str>" } innerHTML contains
//   { "assertCount":  "<sel>", "equals":  N }        number of matches
//   { "assertGlobal": "<path>", "equals": value }     globalThis metric value
//   { "assertStorage": "<key>", "contains": "<str>" } localStorage item contains text
//   { "seedStorage": { "key": "value" } }          pre-mount localStorage entries
//   { "click":        "<sel>" }                      dispatch a click
//   { "dblclick":     "<sel>" }                      dispatch a double click
//   { "input": "<sel>", "value": "<str>" }             set value + dispatch input
//   { "change": "<sel>", "checked": true }              set checked + dispatch change
//   { "blur": "<sel>" }                       dispatch a blur event
//   { "keydown": "<sel>", "key": "Enter" }              dispatch keyboard event
//   { "tick": true }                                 await the microtask queue
//
// The harness registers a minimal $$defer on globalThis so reactive
// updates flush synchronously inside a step (real runtime.js batches
// via microtask; the harness flushes eagerly for deterministic
// assertions, and the "tick" step also drains real microtasks).

import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
import { fixtureStorage, replayFixtureSteps } from './runtime-fixture-dsl.mjs';

// jsdom may be installed in a node_modules dir unrelated to this
// script's location.  When the Go test sets VELA_NODE_MODULES,
// resolve jsdom from there; otherwise fall back to a plain import.
let JSDOM;
{
  const nm = process.env.VELA_NODE_MODULES;
  if (nm) {
    const req = createRequire(nm.endsWith('/') ? nm : nm + '/');
    ({ JSDOM } = req('jsdom'));
  } else {
    ({ JSDOM } = await import('jsdom'));
  }
}

const [, , componentPath, exportName, stepsPath] = process.argv;

function fail(msg) {
  process.stdout.write(JSON.stringify({ ok: false, failures: [msg] }));
  process.exit(0); // exit 0 — the Go side reads the JSON, not the code
}

if (!componentPath || !exportName || !stepsPath) {
  fail('usage: runtime-harness.mjs <component.js> <export> <steps.json>');
}

let steps;
try {
  steps = JSON.parse(readFileSync(stepsPath, 'utf8'));
} catch (e) {
  fail('cannot read steps file: ' + e.message);
}

// ── jsdom environment ────────────────────────────────────────────
const dom = new JSDOM('<!doctype html><html><body></body></html>', {
  pretendToBeVisual: true,
  url: "https://vela.test/",
});
const { window } = dom;

// Expose the DOM globals the compiled component expects.  A compiled
// component only touches `document` and standard DOM constructors.
globalThis.window = window;
globalThis.document = window.document;
globalThis.CustomEvent = window.CustomEvent;
globalThis.Event = window.Event;
globalThis.Node = window.Node;
globalThis.HTMLElement = window.HTMLElement;
globalThis.localStorage = window.localStorage;
globalThis.requestAnimationFrame =
  window.requestAnimationFrame ?? ((fn) => setTimeout(fn, 0));

// Minimal $$defer: run the callback immediately.  The real runtime
// batches into a microtask; for test determinism the harness runs
// updates eagerly so an assertion right after an action sees the
// settled DOM.  A "tick" step still drains real microtasks for code
// paths that schedule their own.
globalThis[Symbol.for('vela.defer')] = (fn) => { fn(); };
for (const [key, value] of Object.entries(fixtureStorage(steps))) {
  window.localStorage.setItem(key, value);
}

// ── load + mount the component ───────────────────────────────────
let failures = [];
let mod;
try {
  mod = await import(pathToFileURL(componentPath).href);
} catch (e) {
  fail('import failed: ' + (e && e.stack ? e.stack : e));
}

const Component = mod[exportName];
if (typeof Component !== 'function') {
  fail(`export '${exportName}' is not a function`);
}

const target = window.document.body;
let instance;
function mount() {
  return Component(target, {}, {});
}
try {
  instance = mount();
} catch (e) {
  fail('mount threw: ' + (e && e.stack ? e.stack : e));
}

// flushMicrotasks drains the microtask queue. Several turns cover defers
// that schedule further defers.
async function flushMicrotasks() {
  for (let turn = 0; turn < 5; turn++) await Promise.resolve();
}

await flushMicrotasks();

const $ = (selector) => window.document.querySelector(selector);
const $$ = (selector) => window.document.querySelectorAll(selector);
function fixtureTarget(selector, action) {
  const element = $(selector);
  if (!element) throw new Error(action + ' target ' + JSON.stringify(selector) + ' not found');
  return element;
}

function globalValue(path) {
  return String(path).split('.').reduce(
    (value, key) => value == null ? undefined : value[key], globalThis);
}

const adapter = {
  text: (selector) => {
    const element = $(selector);
    return element ? element.textContent.trim() : null;
  },
  html: (selector) => {
    const element = $(selector);
    return element ? element.innerHTML : null;
  },
  value: (selector) => {
    const element = $(selector);
    return element ? element.value : null;
  },
  storage: (key) => window.localStorage.getItem(key),
  global: globalValue,
  count: (selector) => $$(selector).length,
  focused: (selector) => window.document.activeElement === $(selector),
  style: (selector, property) => {
    const element = $(selector);
    return element ? window.getComputedStyle(element).getPropertyValue(property) : null;
  },
  focus: (selector) => fixtureTarget(selector, 'focus').focus(),
  click: (selector) => fixtureTarget(selector, 'click').dispatchEvent(
    new window.MouseEvent('click', { bubbles: true, cancelable: true })),
  dblclick: (selector) => fixtureTarget(selector, 'double-click').dispatchEvent(
    new window.MouseEvent('dblclick', { bubbles: true, cancelable: true })),
  submit: (selector) => fixtureTarget(selector, 'submit').dispatchEvent(
    new window.Event('submit', { bubbles: true, cancelable: true })),
  input: (selector, value) => {
    const element = fixtureTarget(selector, 'input');
    element.value = value;
    element.dispatchEvent(new window.Event('input', { bubbles: true, cancelable: true }));
  },
  change: (selector, step) => {
    const element = fixtureTarget(selector, 'change');
    if ('checked' in step) element.checked = !!step.checked;
    if ('value' in step) element.value = step.value;
    element.dispatchEvent(new window.Event('change', { bubbles: true, cancelable: true }));
  },
  blur: (selector) => fixtureTarget(selector, 'blur').dispatchEvent(
    new window.FocusEvent('blur', { bubbles: false, cancelable: false })),
  keydown: (selector, key) => fixtureTarget(selector, 'keydown').dispatchEvent(
    new window.KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })),
  remount: () => {
    if (instance && typeof instance.destroy === 'function') instance.destroy();
    if (typeof target.replaceChildren === 'function') target.replaceChildren();
    else target.textContent = '';
    instance = mount();
  },
  settle: flushMicrotasks,
};

failures = await replayFixtureSteps(steps, adapter);

// ── teardown check ───────────────────────────────────────────────
if (instance && typeof instance.destroy === 'function') {
  try {
    instance.destroy();
  } catch (e) {
    failures.push('destroy() threw: ' + (e && e.stack ? e.stack : e));
  }
}

process.stdout.write(JSON.stringify({
  ok: failures.length === 0,
  failures,
}));
