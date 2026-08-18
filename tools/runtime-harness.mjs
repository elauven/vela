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
//   { "click":        "<sel>" }                      dispatch a click
//   { "input": "<sel>", "value": "<str>" }             set value + dispatch input
//   { "change": "<sel>", "checked": true }              set checked + dispatch change
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
globalThis.requestAnimationFrame =
  window.requestAnimationFrame ?? ((fn) => setTimeout(fn, 0));

// Minimal $$defer: run the callback immediately.  The real runtime
// batches into a microtask; for test determinism the harness runs
// updates eagerly so an assertion right after an action sees the
// settled DOM.  A "tick" step still drains real microtasks for code
// paths that schedule their own.
globalThis[Symbol.for('vela.defer')] = (fn) => { fn(); };

// ── load + mount the component ───────────────────────────────────
const failures = [];
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
try {
  instance = Component(target, {}, {});
} catch (e) {
  fail('mount threw: ' + (e && e.stack ? e.stack : e));
}

// flushMicrotasks drains the microtask queue.  runtime.js's $$defer
// batches reactive DOM updates into a microtask, so assertions must
// wait for it to settle.  Several turns cover defers that schedule
// further defers.
async function flushMicrotasks() {
  for (let t = 0; t < 5; t++) {
    await Promise.resolve();
  }
}

// Settle any update scheduled during initial mount.
await flushMicrotasks();

// ── step replay ──────────────────────────────────────────────────
const $ = (sel) => window.document.querySelector(sel);
const $$ = (sel) => window.document.querySelectorAll(sel);

for (let i = 0; i < steps.length; i++) {
  const step = steps[i];
  const tag = `step ${i}`;
  try {
    if ('assertText' in step) {
      const el = $(step.assertText);
      const got = el ? el.textContent.trim() : null;
      if (got !== step.equals) {
        failures.push(`${tag}: text of '${step.assertText}' = ` +
          `${JSON.stringify(got)}, want ${JSON.stringify(step.equals)}`);
      }
    } else if ('assertHTML' in step) {
      const el = $(step.assertHTML);
      const got = el ? el.innerHTML : null;
      if (got == null || !got.includes(step.contains)) {
        failures.push(`${tag}: html of '${step.assertHTML}' = ` +
          `${JSON.stringify(got)}, want substring ` +
          `${JSON.stringify(step.contains)}`);
      }
    } else if ('assertValue' in step) {
      // The .value PROPERTY of the first match (inputs, textareas,
      // selects) — distinct from textContent / the value attribute.
      const el = $(step.assertValue);
      const got = el ? el.value : null;
      if (got !== step.equals) {
        failures.push(`${tag}: value of '${step.assertValue}' = ` +
          `${JSON.stringify(got)}, want ${JSON.stringify(step.equals)}`);
      }
    } else if ('assertCount' in step) {
      const got = $$(step.assertCount).length;
      if (got !== step.equals) {
        failures.push(`${tag}: count of '${step.assertCount}' = ` +
          `${JSON.stringify(got)}, want ${JSON.stringify(step.equals)}`);
      }
    } else if ('focus' in step) {
      // Move keyboard focus to the first matching element — used
      // to set up the precondition for a later assertFocused check
      // (Session A: verifying that a reactive re-render does not
      // tear out the focused input).
      const el = $(step.focus);
      if (!el) {
        failures.push(`${tag}: focus target '${step.focus}' not found`);
      } else {
        el.focus();
      }
    } else if ('assertFocused' in step) {
      // Assert that the element matched by the selector IS the
      // document's activeElement.  This is THE test for the
      // Session A change: when a parent re-renders an unkeyed
      // {#each} whose array structure is unchanged, the rows are
      // updated in place rather than destroyed, so the focused
      // input survives.  A destroy+rebuild would replace the
      // node and activeElement would move to <body>.
      const want = $(step.assertFocused);
      const got = window.document.activeElement;
      if (!want) {
        failures.push(`${tag}: assertFocused target ` +
          `'${step.assertFocused}' not found`);
      } else if (got !== want) {
        failures.push(`${tag}: activeElement is ` +
          `${got ? got.tagName + (got.className ? '.' + got.className : '') : 'null'}` +
          `, want ${want.tagName}` +
          `${want.className ? '.' + want.className : ''}`);
      }
    } else if ('click' in step) {
      const el = $(step.click);
      if (!el) {
        failures.push(`${tag}: click target '${step.click}' not found`);
      } else {
        el.dispatchEvent(new window.MouseEvent('click', {
          bubbles: true, cancelable: true,
        }));
        // runtime.js's $$defer batches reactive updates into a
        // microtask; drain it so the DOM has settled before the
        // next assertion.  A few turns covers chained defers.
        await flushMicrotasks();
      }
    } else if ("submit" in step) {
      const el = $(step.submit);
      if (!el) {
        failures.push(tag + ": submit target " + step.submit + " not found");
      } else {
        el.dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
        await flushMicrotasks();
      }
    } else if ("input" in step) {
      const el = $(step.input);
      if (!el) {
        failures.push(tag + ": input target " + step.input + " not found");
      } else {
        el.value = step.value ?? "";
        el.dispatchEvent(new window.Event("input", { bubbles: true, cancelable: true }));
        await flushMicrotasks();
      }
    } else if ("change" in step) {
      const el = $(step.change);
      if (!el) {
        failures.push(tag + ": change target " + step.change + " not found");
      } else {
        if ("checked" in step) el.checked = !!step.checked;
        if ("value" in step) el.value = step.value;
        el.dispatchEvent(new window.Event("change", { bubbles: true, cancelable: true }));
        await flushMicrotasks();
      }
    } else if ("keydown" in step) {
      const el = $(step.keydown);
      if (!el) {
        failures.push(tag + ": keydown target " + step.keydown + " not found");
      } else {
        el.dispatchEvent(new window.KeyboardEvent("keydown", {
          key: step.key ?? "", bubbles: true, cancelable: true,
        }));
        await flushMicrotasks();
      }
    } else if ("tick" in step) {
      await flushMicrotasks();
    } else {
      failures.push(`${tag}: unknown step ${JSON.stringify(step)}`);
    }
  } catch (e) {
    failures.push(`${tag}: threw ${e && e.stack ? e.stack : e}`);
  }
}

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
