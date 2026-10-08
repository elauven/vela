package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeStoreLifecycleContract(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	// The runtime keeps its staged name and the directory is marked as ES
	// modules.
	for name, source := range map[string]string{
		"runtime.js":   "runtime/runtime.js",
		"package.json": "",
	} {
		data := []byte(`{"type":"module"}`)
		if source != "" {
			var err error
			if data, err = os.ReadFile(source); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	const script = `import { $$defer, $$isProxy, $$proxy, $$readSelectValues, $$selectValuesEqual, $$writeSelectValues, derived, readable, writable } from './runtime.js';

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

const selectedWrites = [];
const select = {
  options: [
    { value: 'one', selected: true },
    { value: 2, selected: false },
    { value: 'one', selected: true },
  ],
};
for (const option of select.options) {
  let selected = option.selected;
  Object.defineProperty(option, 'selected', {
    get() { return selected; },
    set(value) { selectedWrites.push([option.value, value]); selected = value; },
    enumerable: true,
  });
}
assert($$readSelectValues(select).join(',') === 'one,one',
  'multi-select reads selected values in DOM order');
assert($$selectValuesEqual(select, ['one']),
  'multi-select adoption equality ignores duplicate option values');
assert(!$$selectValuesEqual(select, ['one', 'missing']),
  'multi-select adoption equality detects an unrepresented model value');
assert(!$$writeSelectValues(select, ['one', 'one']),
  'multi-select writer must skip every native write for an equal selected set');
assert(selectedWrites.length === 0,
  'multi-select equal-set write must not touch option.selected');
assert($$writeSelectValues(select, [2]),
  'multi-select writer reports a changed selected set');
assert(selectedWrites.map(write => write.join(':')).join(',') === 'one:false,2:true,one:false',
  'multi-select writer updates only options whose membership changed');
assert($$readSelectValues(select).join(',') === '2',
  'multi-select writer normalizes model values to strings');
assert($$writeSelectValues(select, undefined),
  'multi-select writer treats a missing model as an empty selected set');
assert(selectedWrites.map(write => write.join(':')).join(',') === 'one:false,2:true,one:false,2:false',
  'multi-select missing-model write clears only the selected option');

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
