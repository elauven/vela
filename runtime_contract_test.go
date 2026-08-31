package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeCompatibilityEntryReExportsCanonicalRuntime(t *testing.T) {
	data, err := os.ReadFile("runtime/runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(data)), "export * from '../runtime.js';"; !strings.HasSuffix(got, want) {
		t.Fatalf("compatibility runtime must re-export canonical runtime, got %q", got)
	}
}

func TestRuntimeStoreLifecycleContract(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	runtime, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.mjs"), runtime, 0o644); err != nil {
		t.Fatal(err)
	}

	const script = `import { $$acquireHydrationRange, $$createHydrationWalker, $$defer, $$isProxy, $$proxy, $$readHydrationPayload, derived, readable, writable } from './runtime.mjs';

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

let starts = 0;
let stops = 0;
const source = readable(0, set => {
  starts++;
  set(starts);
  return () => { stops++; };
});
const firstValues = [];
const secondValues = [];
const unFirst = source.subscribe(value => firstValues.push(value));
const unSecond = source.subscribe(value => secondValues.push(value));
assert(starts === 1, 'readable start must run only for its first subscriber');
unFirst();
unFirst();
assert(stops === 0, 'readable cleanup must wait for its final subscriber');
unSecond();
assert(stops === 1, 'readable cleanup must run once after final unsubscribe');
const unThird = source.subscribe(() => {});
assert(starts === 2, 'readable must restart after a new first subscriber');
unThird();
assert(stops === 2, 'readable restart cleanup must run once');

let sourceStarts = 0;
let sourceStops = 0;
const upstream = readable(2, () => {
  sourceStarts++;
  return () => { sourceStops++; };
});
let computations = 0;
const doubled = derived(upstream, value => {
  computations++;
  return value * 2;
});
assert(sourceStarts === 0, 'derived must not subscribe eagerly');
const unDerived = doubled.subscribe(value => {
  assert(value === 4, 'derived initial value must be computed from upstream');
});
assert(sourceStarts === 1 && computations === 1,
  'derived must start upstream once for its first subscriber');
unDerived();
assert(sourceStops === 1, 'derived must stop upstream after final unsubscribe');
const unDerivedAgain = doubled.subscribe(() => {});
assert(sourceStarts === 2, 'derived must restart upstream for a new subscriber');
doubled.destroy();
doubled.destroy();
assert(sourceStops === 2, 'derived destroy must be idempotent');
unDerivedAgain();

const faulty = writable(0);
let throwOnUpdate = false;
const expected = new Error('subscriber error');
const observed = [];
faulty.subscribe(() => {
  if (throwOnUpdate) throw expected;
});
faulty.subscribe(value => observed.push(value));
throwOnUpdate = true;
let caught;
try {
  faulty.set(1);
} catch (error) {
  caught = error;
}
assert(caught === expected, 'writable must preserve a subscriber error');
assert(observed.join(',') === '0,1',
  'a faulty subscriber must not block later subscribers');

let deferredError;
let ranAfterError = false;
let ranReentrant = false;
process.once('uncaughtException', error => { deferredError = error; });
$$defer(() => {
  $$defer(() => { ranReentrant = true; });
  throw expected;
});
$$defer(() => { ranAfterError = true; });
await new Promise(resolve => setTimeout(resolve, 0));
assert(deferredError === expected, 'scheduler must report its first callback error');
assert(ranAfterError && ranReentrant,
  'scheduler must drain later and reentrant callbacks after an error');

const rawDeep = { nested: { value: 1 }, date: new Date(0) };
rawDeep.self = rawDeep;
let deepNotifications = 0;
const deep = $$proxy(rawDeep, () => { deepNotifications++; });
assert($$isProxy(deep), 'deep root must be branded as a Vela proxy');
assert(deep.nested === deep.nested, 'deep nested reads must preserve proxy identity');
assert(deep.self === deep, 'deep cyclic references must resolve to the root proxy');
assert(deep.date === rawDeep.date, 'built-in objects with internal slots must remain unwrapped');
let beforeDeepMutation = deepNotifications;
deep.nested.value = 2;
assert(deepNotifications === beforeDeepMutation + 1, 'nested write must notify exactly once');
beforeDeepMutation = deepNotifications;
delete deep.nested.value;
assert(deepNotifications === beforeDeepMutation + 1, 'nested delete must notify exactly once');
beforeDeepMutation = deepNotifications;
deep.nested.missing = undefined;
assert(deepNotifications === beforeDeepMutation + 1,
  'adding an absent key must notify even when its value is undefined');
beforeDeepMutation = deepNotifications;
deep.nested.missing = undefined;
assert(deepNotifications === beforeDeepMutation,
  'equal nested write must not notify');
beforeDeepMutation = deepNotifications;
delete deep.nested.missing;
assert(deepNotifications === beforeDeepMutation + 1,
  'deleting an existing nested key must notify');
beforeDeepMutation = deepNotifications;
delete deep.nested.missing;
assert(deepNotifications === beforeDeepMutation,
  'deleting a missing nested key must not notify');
beforeDeepMutation = deepNotifications;
deep.nested.items = [];
assert(deepNotifications === beforeDeepMutation + 1,
  'adding a nested array must notify once');
beforeDeepMutation = deepNotifications;
deep.nested.items.push('first');
assert(deepNotifications === beforeDeepMutation + 1,
  'one Array#push must notify once, not once per Proxy trap');
beforeDeepMutation = deepNotifications;
deep.nested.items.splice(0, 0, 'zero');
assert(deepNotifications === beforeDeepMutation + 1,
  'one Array#splice must notify once, not once per Proxy trap');

function marker(data) { return { data }; }
function markerRoot(markers, payloads = {}) {
  const doc = {
    createTreeWalker() {
      let index = 0;
      return { nextNode() { return index < markers.length ? markers[index++] : null; } };
    },
    getElementById(id) { return payloads[id] || null; },
  };
  return { ownerDocument: doc };
}
const hydratedRoot = markerRoot([marker('v0'), marker('v1'), marker('/v1'), marker('/v0')]);
const walker = $$createHydrationWalker(hydratedRoot, 'vela-test');
const outer = $$acquireHydrationRange(walker, 0);
const inner = $$acquireHydrationRange(walker, 1);
assert(outer.start.data === 'v0' && outer.end.data === '/v0', 'walker must retain outer paired range');
assert(inner.start.data === 'v1' && inner.end.data === '/v1', 'walker must retain nested paired range');
let mismatch;
try { $$createHydrationWalker(markerRoot([marker('v0'), marker('/v1')]), 'vela-test'); }
catch (error) { mismatch = error; }
assert(mismatch && mismatch.message.includes('expected <!--/v0--> before <!--/v1-->'),
  'walker must diagnose crossed marker pairs');
mismatch = undefined;
try { $$acquireHydrationRange(walker, 9); }
catch (error) { mismatch = error; }
assert(mismatch && mismatch.message.includes('missing marker range v9'),
  'walker must diagnose a missing requested range');
const payloadRoot = markerRoot([], {
  'vela-state-vela-test': { type: 'application/json', textContent: '{"component":"vela-test","props":{"Title":"ok"},"state":{},"incomplete":false}' },
});
const payload = $$readHydrationPayload(payloadRoot, 'vela-test');
assert(payload.props.Title === 'ok', 'payload reader must return matching inert payload');
payloadRoot.ownerDocument.getElementById = () => ({ type: 'application/json', textContent: '{"component":"vela-test","incomplete":true,"omitted":["now"]}' });
mismatch = undefined;
try { $$readHydrationPayload(payloadRoot, 'vela-test'); }
catch (error) { mismatch = error; }
assert(mismatch && mismatch.message.includes('state payload is incomplete; omitted state: now'),
  'payload reader must reject incomplete state restoration');

console.log('OK');

`
	if err := os.WriteFile(filepath.Join(dir, "contract.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "contract.mjs")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime store contract failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "OK" {
		t.Fatalf("unexpected runtime store contract output: %q", got)
	}
}
