package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSRProjectWritesExecutableHydrationClientForSupportedSubset(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "Page.vela")
	if err := os.WriteFile(source, []byte(`<script>export let title='server';</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	clientPath := layout.hydrationClientPath(source, srcRoot)
	client, err := os.ReadFile(clientPath)
	if err != nil {
		t.Fatalf("read hydration client: %v", err)
	}
	if !strings.Contains(string(client), "$$acquireHydrationRange($$walker, 0)") {
		t.Fatalf("hydration client misses text acquire:\n%s", client)
	}
	entry, err := os.ReadFile(layout.hydratePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "import $$client from './Page.hydrate_client.js';") {
		t.Fatalf("hydrate entry does not invoke the supported client:\n%s", entry)
	}
}

func TestCanBuildSSRHydrationClientSubset(t *testing.T) {
	for _, source := range []struct {
		name string
		code string
		want bool
	}{
		{"root attributes and event", `<template><button class:active={selected} @click={toggle}>Save</button></template>`, true},
		{"direct dynamic text", `<template><p>{title}</p></template>`, true},
		{"nested dynamic text", `<template><main><p>{title}</p></main></template>`, true},
		{"nested dynamic attribute", `<template><main><button :disabled={saving}>Save</button></main></template>`, true},
		{"direct static if", `<template><main>{#if visible}<span>On</span>{:else}<span>Off</span>{/if}</main></template>`, true},
		{"if with direct child lifecycle", `<template><main>{#if visible}<Child :title={title} />{:else}<span>Off</span>{/if}</main></template>`, true},
		{"if child lifecycle forwards component event", `<template><main>{#if visible}<Child @on:close={close} />{:else}<span>Off</span>{/if}</main></template>`, true},
		{"nested static if and each", `<template><main><section>{#if visible}<span>On</span>{:else}<span>Off</span>{/if}{#each items as item}<span>{item}</span>{/each}</section></main></template>`, true},
		{"direct static key", `<template><main>{#key identity}<span>Row</span>{/key}</main></template>`, true},
		{"if with dynamic branch", `<template><main>{#if visible}<span>{title}</span>{/if}</main></template>`, true},
		{"direct static each", `<template><main>{#each items as item}<span>Row</span>{/each}</main></template>`, true},
		{"static each else", `<template><main>{#each items as item}<span>Row</span>{:else}<span>Empty</span>{/each}</main></template>`, true},
		{"dynamic text each row", `<template><main>{#each items as item}<span>{item}</span>{/each}</main></template>`, true},
		{"keyed each", `<template><main>{#each items as item (item.id)}<span>Row</span>{/each}</main></template>`, true},
		{"keyed each else", `<template><main>{#each items as item (item.id)}<span>Row</span>{:else}<span>Empty</span>{/each}</main></template>`, false},
		{"static await", `<template><main>{#await request}<span>Loading</span>{:then value}<span>Done</span>{:catch error}<span>Failed</span>{/await}</main></template>`, true},
		{"await with dynamic resolution", `<template><main>{#await request}<span>Loading</span>{:then value}<span>{value}</span>{/await}</main></template>`, true},
		{"await with dynamic event", `<template><main>{#await request}<span>Loading</span>{:then value}<button @click={save}>{value}</button>{/await}</main></template>`, true},
		{"await with static each", `<template><main>{#await request}<span>Loading</span>{:then value}{#each items as item}<span>{item}</span>{/each}{/await}</main></template>`, true},
		{"await with direct child lifecycle", `<template><main>{#await request}<Child :title={title} />{:then value}<Child :title={value} />{:catch error}<Child :title={error} />{/await}</main></template>`, true},
		{"await pending nested if child lifecycle", `<template><main>{#await request}{#if visible}<Child :title={title} />{:else}<span>Waiting</span>{/if}{:then value}<span>{value}</span>{/await}</main></template>`, true},
		{"await pending nested key child lifecycle", `<template><main>{#await request}{#key identity}<Child :title={title} />{/key}{:then value}<span>{value}</span>{/await}</main></template>`, true},
		{"await pending unkeyed each child lifecycle", `<template><main>{#await request}{#each items as item}<Child :title={item.title} />{/each}{:then value}<span>{value}</span>{/await}</main></template>`, true},
		{"await pending keyed each child lifecycle", `<template><main>{#await request}{#each rows as row (row.id)}<Child :title={row.title} />{/each}{:then value}<span>{value}</span>{/await}</main></template>`, true},
		{"await then nested if child lifecycle", `<template><main>{#await request}<p>Loading</p>{:then value}{#if visible}<Child :title={value} />{:else}<p>Hidden</p>{/if}{/await}</main></template>`, true},
		{"await child lifecycle forwards component event", `<template><main>{#await request}<Child @on:close={close} />{:then value}<p>Done</p>{/await}</main></template>`, true},
		{"each block", `<template>{#each items as item}<p>{item}</p>{/each}</template>`, false},
		{"binding", `<template><input bind:value={query} /></template>`, true},
		{"select binding", `<template><select bind:value={choice}><option value="a">A</option></select></template>`, true},
		{"element reference", `<template><input bind:this={input} /></template>`, true},
	} {
		t.Run(source.name, func(t *testing.T) {
			sfc, err := parseSFC(source.code)
			if err != nil {
				t.Fatal(err)
			}
			if got := CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes); got != source.want {
				t.Fatalf("CanBuildSSRHydrationClient()=%t, want %t", got, source.want)
			}
		})
	}
}

func TestSSRHydrationKeyedEachAwaitUsesPerRowState(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}{#await row.request}<Child :title={row.title} />{:then value}<Child :title={value} />{:catch error}<Child :title={error} />{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{"_await_states", "adoptHydrationKeyedRowRanges", "reconcileHydrationKeyedRowRanges", "replaceHydrationKeyedRowContents", ".cell !==", "_destroy"} {
		if !strings.Contains(source, want) {
			t.Fatalf("keyed each-await lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitSettledTextUsesRowOwnedDOMUpdater(t *testing.T) {
	sfc, err := parseSFC(`<script>export let rows = []; let prefix = $state("ready: ");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Loading</p>{:then value}{prefix}{value}<p class="marker" :data-label={prefix}>Done</p>{:catch error}{prefix}{error}<p class="marker" :data-label={prefix}>Failed</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{"updates", "createTextNode", "_update", "replaceHydrationKeyedRowContents"} {
		if !strings.Contains(source, want) {
			t.Fatalf("keyed each-await direct text lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationClientAdoptsPendingAwaitRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request;</script><template><main>{#await request}<span>Loading</span>{:then value}<span>{value}</span>{:catch error}<span>{error}</span>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$hawait0_range = $$acquireHydrationRange($$walker, 0);",
		"let $$hawait0_nodes = $$adoptHydrationRange($$hawait0_range);",
		"function $$hawait0_render()",
		"function $$hawait0_drive()",
		"Promise.resolve(request)",
		"String($$hawait0_value)",
		"String($$hawait0_error)",
		"$$hawait0_range.end.before($$frag)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration await lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "appendChild($$root)") || strings.Contains(source, "document.createElement('main')") {
		t.Fatalf("hydration await lowering unexpectedly creates initial DOM:\n%s", source)
	}
}

func TestSSRHydrationAwaitChildBranchesAdoptAndMountWithCSRFactories(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let title = "pending";</script><template><main>{#await request}<p class="before">Waiting</p><Child :title={title} /><p class="after">Soon</p>{:then value}<Child :title={value} />{:catch error}<Child :title={error} />{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`import { Child as $$csr_Child } from './Child.js';`,
		`import { Child } from './Child.hydrate.js';`,
		`const $$hawait0_range = $$acquireHydrationRange($$walker, 0);`,
		`$$hawait0_b0_component_0 = Child($$root, null,`,
		`$$hawait0_b1_component_0 = $$csr_Child($$frag, { title: $$hawait0_value }, {});`,
		`$$hawait0_b2_component_0 = $$csr_Child($$frag, { title: $$hawait0_error }, {});`,
		`$$hawait0_b0_component_0.destroy();`,
		`$$hawait0_b1_component_0.destroy();`,
		`$$hawait0_b2_component_0.destroy();`,
		`$$hawait0_range.end.before($$frag);`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("await child lifecycle misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, `$$csr_Child($$root`) {
		t.Fatalf("initial pending child must be adopted rather than CSR-mounted:\n%s", source)
	}
}

func TestSSRHydrationAwaitSnapshotAdoptsSettledChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; function close() {}</script><template><main>{#await request}<p>Waiting</p>{:then value}<Child :title={value} @on:close={close} /><p>Between</p><Child :title={value} @on:close={close} /><p>Done</p>{:catch error}<Child :title={error} @on:close={close} /><p>Between failed</p><Child :title={error} @on:close={close} /><p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`const $$hawait0_snapshot = $$props.$$velaAwaitAt && $$props.$$velaAwaitAt[$$instanceID + ':0'] || $$props.$$velaAwait && $$props.$$velaAwait[0];`,
		`$$hawait0_snapshot.state === 'then'`,
		`$$hawait0_b1_component_0 = Child($$root, null,`,
		`$$hawait0_b1_component_0.set({ title: $$hawait0_value });`,
		`$$hawait0_b1_component_0.el.addEventListener('close', $$hawait0_b1_component_0_on_close_h);`,
		`$$hawait0_b1_component_0.el.removeEventListener('close', $$hawait0_b1_component_0_on_close_h);`,
		`$$hawait0_b1_component_1 = Child($$root, null,`,
		`$$hawait0_b1_component_1.set({ title: $$hawait0_value });`,
		`$$hawait0_b1_component_1.el.addEventListener('close', $$hawait0_b1_component_1_on_close_h);`,
		`$$hawait0_b1_component_1.el.removeEventListener('close', $$hawait0_b1_component_1_on_close_h);`,
		`let $$hawait0_initial = true;`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("await snapshot child lifecycle misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, `$$csr_Child($$root`) {
		t.Fatalf("settled snapshot child must be adopted rather than CSR-mounted:\n%s", source)
	}
	firstSet := strings.Index(source, `$$hawait0_b1_component_0.set({ title: $$hawait0_value });`)
	secondSet := strings.Index(source, `$$hawait0_b1_component_1.set({ title: $$hawait0_value });`)
	secondDestroy := strings.Index(source, `$$hawait0_b1_component_1.destroy();`)
	firstDestroy := strings.Index(source, `$$hawait0_b1_component_0.destroy();`)
	if firstSet < 0 || secondSet < firstSet || secondDestroy < 0 || firstDestroy < secondDestroy {
		t.Fatalf("await snapshot child chain lost source/update or reverse destroy order:\n%s", source)
	}
}

func TestSSRHydrationAwaitSnapshotAdoptsBindableChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; let draft = $state("server");</script><template><main>{#await request}<p>Waiting</p>{:then value}<Child bind:value={draft} /><p>Done</p>{:catch error}<p>Failed</p>{/await}<p>{draft}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("await snapshot bindable child was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`Child($$root, { $$onChange_value: ($$v) => $$set_draft($$v) }).component`,
		`$$hawait0_b1_component_0.set({ value: draft, $$onChange_value: ($$v) => $$set_draft($$v) });`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("await snapshot bindable child lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, `Child($$root, { value: draft`) {
		t.Fatalf("await snapshot child hydration must not replay bound server prop:\n%s", source)
	}
}

func TestSSRHydrationAwaitSnapshotAdoptsStaticSlotChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request;</script><template><main>{#await request}<p>Waiting</p>{:then value}<Child><p class="slotted">Body</p></Child><p>Done</p>{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("await snapshot static-slot child was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`Child($$root).component`,
		`$$csr_Child($$frag, {}, { 'default': () => {`,
		`$$insertHTML($$sfrag_`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("await snapshot static-slot child lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotAdoptsReactiveSlotLeafChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let title = "server"; export let active = false; export let color = "red"; export let draft = "server"; export let enabled = false; function increment() {}</script><template><main>{#await request}<p>Waiting</p>{:then value}<Child><p class="slotted" :title={title} class:active={active} style:color={color}>{title}</p><button @click={increment}>Increment</button><input bind:value={draft} /><input type="checkbox" bind:checked={enabled} /></Child><p>Done</p>{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("await snapshot reactive-slot child was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`Child($$root).component`,
		`$$acquireHydrationElement($$walker,`,
		`getAttribute('title')`,
		`setAttribute('title'`,
		`classList.toggle('active'`,
		`style.setProperty('color'`,
		`addEventListener('click', increment)`,
		`removeEventListener('click', increment)`,
		`addEventListener('input'`,
		`addEventListener('change'`,
		`$$set_draft(`,
		`$$set_enabled(`,
		`$$acquireHydrationRange($$walker, 1)`,
		`$$writeHydrationText`,
		`$$csr_Child($$frag, {}, { 'default': () => {`,
		`document.createElement('p')`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("await snapshot reactive-slot child lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitThenNestedIfOwnsChildCleanup(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let visible = true;</script><template><main>{#await request}<p>Loading</p>{:then value}{#if visible}<Child :title={value} />{:else}<p>Hidden</p>{/if}{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`import { Child as $$csr_Child } from './Child.js';`,
		`$$n3_comp = $$csr_Child(`,
		`$$n3_comp.destroy();`,
		`if ($$hawait0_active === 1) {`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested await-if ownership misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationPendingAwaitNestedIfAdoptsChildAndOwnsTeardown(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let shown = true; export let title = "pending";</script><template><main>{#await request}{#if shown}<Child :title={title} />{:else}<p>Waiting</p>{/if}{:then value}<p>{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`import { Child as ` + "$" + "$" + `csr_Child } from './Child.js';`,
		`import { Child } from './Child.hydrate.js';`,
		`acquireHydrationRange(` + "$" + "$" + `walker, 0)`,
		`Child(` + "$" + "$" + `root).component`,
		`.destroy();`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("pending await-if adoption misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, `$$csr_Child($$root`) {
		t.Fatalf("initial nested pending child must be adopted rather than CSR-mounted:\n%s", source)
	}
}

func TestSSRHydrationEachNestedIfAdoptsChildRowsAndBuildsCSRReturnPath(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row}{#if row.visible}<Child :title={row.title} />{:else}<p>Hidden</p>{/if}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{
		"acquireHydrationRange(" + dollars + "walker, 0)",
		"Child(" + dollars + "root, null,",
		"(" + dollars + "item.visible ? 0 : 1) === 0",
		"createDocumentFragment",
		".destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested each-if hydration misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, dollars+"csr_Child("+dollars+"root") {
		t.Fatalf("initial nested each-if child must be adopted rather than CSR-mounted:\n%s", source)
	}
}

func TestSSRHydrationIfNestedAwaitAdoptsChildAndBuildsCSRReturnPath(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let shown = true; export let request;</script><template><main>{#if shown}{#await request}<Child :title={"pending"} />{:then value}<Child :title={value} />{/await}{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{"acquireHydrationRange(" + dollars + "walker, 0)", "acquireHydrationRange(" + dollars + "walker, 1)", "Child(" + dollars + "root", dollars + "csr_Child(", ".destroy();"} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested if-await hydration misses %q:\n%s", want, source)
		}
	}
}
func TestSSRHydrationKeyNestedAwaitAdoptsChildAndBuildsCSRReturnPath(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let identity = "one"; export let request;</script><template><main>{#key identity}{#await request}<Child :title={"pending"} />{:then value}<Child :title={value} />{/await}{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{"acquireHydrationRange(" + dollars + "walker, 0)", "acquireHydrationRange(" + dollars + "walker, 1)", "Child(" + dollars + "root", dollars + "csr_Child(", ".destroy();"} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested key-await hydration misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationEachNestedKeyAdoptsAndBuildsCSRReturnPath(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row}{#key row.version}<Child :title={row.title} />{/key}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{"Child(" + dollars + "root, null,", dollars + "csr_Child(" + dollars + "frag", "--key-", ".destroy();"} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested each-key hydration misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyedEachNestedKeyAdoptsAndReconcilesChildRows(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}{#key row.version}<Child :id={row.id} :title={row.title} />{/key}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{"adoptHydrationKeyedRowRanges", "reconcileHydrationKeyedRowRanges", "replaceHydrationKeyedRowContents", "_inner_keys", "--key-", ".set({ id:", ".destroy();"} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested keyed each-key hydration misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyedEachNestedIfAdoptsAndReconcilesChildRows(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}{#if row.visible}<Child :id={row.id} :title={row.title} />{:else}<p>Hidden</p>{/if}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"adoptHydrationKeyedRowRanges",
		"reconcileHydrationKeyedRowRanges",
		"replaceHydrationKeyedRowContents",
		"new Map()",
		".set({ id:",
		".destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested keyed each-if hydration misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationIfNestedEachAdoptsChildRowsAndBuildsCSRReturnPath(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let shown = true; export let rows = [];</script><template><main>{#if shown}{#each rows as row}<Child :title={row.title} />{/each}{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{
		"acquireHydrationRange(" + dollars + "walker, 0)",
		"Child(" + dollars + "root, null,",
		"if (" + dollars + "hif0_active === 0)",
		"createDocumentFragment",
		".destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested if-each hydration misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, dollars+"csr_Child("+dollars+"root") {
		t.Fatalf("initial nested if-each child must be adopted rather than CSR-mounted:\n%s", source)
	}
}

func TestSSRHydrationIfNestedKeyedEachAdoptsAndBuildsCSRReturnPath(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let shown = true; export let rows = [];</script><template><main>{#if shown}{#each rows as row (row.id)}<Child :id={row.id} :title={row.title} />{/each}{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{
		"adoptHydrationKeyedRowRanges",
		"reconcileHydrationKeyedRowRanges",
		"new Map()",
		"if (" + dollars + "hif0_active === 0)",
		"createDocumentFragment",
		".destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested if-keyed each hydration misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationPendingAwaitUnkeyedEachAdoptsChildRows(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let items = [];</script><template><main>{#await request}{#each items as item}<Child :title={item.title} />{/each}{:then value}<p>{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{
		"acquireHydrationRange(" + dollars + "walker, 0)",
		"for (const " + dollars + "item of",
		".push(Child(" + dollars + "root, null,",
		".destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("pending await-each adoption misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, dollars+"csr_Child("+dollars+"root") {
		t.Fatalf("initial nested pending each child must be adopted rather than CSR-mounted:\n%s", source)
	}
}

func TestSSRHydrationPendingAwaitKeyedEachAdoptsChildRows(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let rows = [];</script><template><main>{#await request}{#each rows as row (row.id)}<Child :title={row.title} />{/each}{:then value}<p>{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{
		"adoptHydrationKeyedRowRanges",
		"reconcileHydrationKeyedRowRanges",
		"new Map()",
		".push(Child(" + dollars + "root, null,",
		".destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("pending await-keyed each adoption misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, dollars+"csr_Child("+dollars+"root") {
		t.Fatalf("initial keyed rows must be adopted rather than CSR-mounted:\n%s", source)
	}
}

func TestSSRHydrationPendingAwaitNestedKeyAdoptsChildWithKeyScope(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let identity = "one"; export let title = "pending";</script><template><main>{#await request}{#key identity}<Child :title={title} />{/key}{:then value}<p>{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	dollars := string([]byte{36, 36})
	for _, want := range []string{
		"--key-",
		"Child(" + dollars + "root, null,",
		".destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("pending await-key adoption misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, dollars+"csr_Child("+dollars+"root") {
		t.Fatalf("initial nested pending key child must be adopted rather than CSR-mounted:\n%s", source)
	}
}

func TestSSRHydrationClientAdoptsStaticEachRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items=[];</script><template><main>{#each items as item}<span>Row</span>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$acquireHydrationRange($$walker, 0)",
		"$$adoptHydrationRange($$heach0_range)",
		"function $$heach0_update()",
		"const $$heach0_items = items;",
		"for (const $$item of $$heach0_items)",
		"$$heach0_range.end.before($$frag)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration each lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "\n  $$heach0_update();\n") || strings.Contains(source, "appendChild($$root)") {
		t.Fatalf("hydration each lowering unexpectedly performs initial DOM work:\n%s", source)
	}
}

func TestSSRHydrationClientBuildsDynamicEachRowsFromAST(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items=[];</script><template><main>{#each items as item}<span class="row">{item}</span>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$row = document.createElement('span');",
		"$$row.appendChild(document.createTextNode(String($$item)));",
		"$$frag.appendChild($$row);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("dynamic each row lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationClientAdoptsKeyedEachRowsFromCachedAST(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items=[];</script><template><main>{#each items as item, index (item.id + ':' + index)}<button class="row">{item.label}</button>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	block := sfc.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*EachBlockNode)
	if block.KeyAST == nil {
		t.Fatal("parser did not cache the keyed each expression")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$adoptHydrationKeyedRows($$heach0_range, $$heach0_items, $$heach0_key, 'button')",
		"const $$heach0_key = ($$item, $$index) => String($$item.id + ':' + $$index);",
		"const $$heach0_create = ($$item, $$index) => {",
		"$$row.appendChild(document.createTextNode(String($$item.label)));",
		"$$row.textContent = String($$item.label);",
		"$$clearHydrationKeyedRowMarkers($$heach0_range);",
		"$$heach0_blocks = $$reconcileHydrationKeyedRows($$heach0_range, $$heach0_blocks, items, $$heach0_key, $$heach0_create, $$heach0_row_update);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("keyed hydration lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "\n  $$heach0_update();\n") || strings.Contains(source, "appendChild($$root)") {
		t.Fatalf("keyed hydration lowering unexpectedly performs initial DOM work:\n%s", source)
	}
}

func TestSSRHydrationClientAdoptsStaticIfRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let visible=false;</script><template><main>{#if visible}<span>On</span>{:else}<span>Off</span>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$acquireHydrationRange($$walker, 0)",
		"$$adoptHydrationRange($$hif0_range)",
		"function $$hif0_update()",
		"$$hif0_nodes = Array.from($$frag.childNodes)",
		"$$hif0_range.end.before($$frag)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration if lowering misses %q:\n%s", want, source)
		}
	}
	for _, forbidden := range []string{
		"\n  $$hif0_update();\n",
		"appendChild($$root)",
		"document.createElement('main')",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("hydration if lowering unexpectedly performs initial DOM work through %q:\n%s", forbidden, source)
		}
	}
}

func TestSSRHydrationClientAdoptsStaticKeyRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let identity='a';</script><template><main>{#key identity}<span class="row">Row</span>{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$adoptHydrationRange($$hkey0_range)",
		"let $$hkey0_prev = identity;",
		"function $$hkey0_update()",
		"if ($$key === $$hkey0_prev)",
		"$$hkey0_range.end.before($$frag)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration key lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "\n  $$hkey0_update();\n") || strings.Contains(source, "appendChild($$root)") {
		t.Fatalf("hydration key lowering unexpectedly performs initial DOM work:\n%s", source)
	}
}

func TestSSRHydrationClientAdoptsNestedTextRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title='server';</script><template><main><section><p class="title">{title}</p></section></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$n1 = $$acquireHydrationElement($$walker, 1, 'section');",
		"const $$n2 = $$acquireHydrationElement($$walker, 2, 'p');",
		"const $$h0_range = $$acquireHydrationRange($$walker, 0);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested text hydration lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "appendChild($$root)") || strings.Contains(source, "document.createElement('section')") {
		t.Fatalf("nested text hydration lowering unexpectedly creates DOM:\n%s", source)
	}
}

func TestSSRHydrationClientDefersNestedAttributeWriteUntilUpdate(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title='server';</script><template><main><p :title={title}>{title}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$n1 = $$acquireHydrationElement($$walker, 1, 'p');",
		"$$n1.setAttribute('title', String(title));",
		"$$writeHydrationText($$h0_range, String(title));",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested attribute hydration lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "\n  $$n1.setAttribute('title', String(title));\n") {
		t.Fatalf("nested attribute hydration lowering writes before a reactive update:\n%s", source)
	}
}

func TestSSRHydrationClientAssignsBindThisAfterAdoption(t *testing.T) {
	sfc, err := parseSFC(`<script>let input=$state(null);</script><template><input bind:this={input} /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$root = $$acquireHydrationElement($$walker, 0, 'input');",
		"$$set_input($$root);",
		"$$set_input(null);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("bind:this hydration lowering misses %q:\n%s", want, source)
		}
	}
}

func TestBrowserSSRHydrationAwaitRetainsPendingThenReplacesRange(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let resolveRequest, rejectRequest; let draft = $state("server"); let visible = $state(false); let items = $state(["A"]); let keyed = $state([{ id: "a", label: "A" }, { id: "b", label: "B" }]); function createRequest() { return new Promise((resolve, reject) => { resolveRequest = resolve; rejectRequest = reject; window.__resolveRequest = resolve; window.__rejectRequest = reject; }); } let request = $state(createRequest()); function restartRequest() { request = createRequest(); } window.__restartRequest = restartRequest; function save() { window.__saved = (window.__saved || 0) + 1; } function toggle() { visible = !visible; } function addItem() { items = [...items, "B"]; } function reorder() { keyed = [keyed[1], keyed[0]]; }</script><template><main>{#await request}<span class="pending">Loading</span>{:then value}<button class="done" :title={value} @click={save}>{value}</button><input class="draft" bind:value={draft} /><p class="draft-output">{draft}</p><button class="toggle" @click={toggle}>Toggle</button>{#if visible}<p class="branch">On</p>{:else}<p class="branch">Off</p>{/if}<button class="add" @click={addItem}>Add</button>{#each items as item}<p class="row">{item}</p>{/each}<button class="reorder" @click={reorder}>Reorder</button>{#each keyed as item (item.id)}<button class="keyed-row">{item.label}</button>{/each}{:catch error}<span class="failed">{error}</span>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-page:0--><main vela-page=""><!--v:vela-page:0--><span vela-page="" class="pending">Loading</span><!--/v:vela-page:0--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from "./Page.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__rootBefore = document.querySelector("main");
  if (new URLSearchParams(location.search).has("mismatch")) {
    const mismatched = document.createElement("section");
    document.querySelector("main").replaceWith(mismatched);
  }
  if (new URLSearchParams(location.search).has("payload")) {
    document.getElementById("vela-state-vela-page").textContent = JSON.stringify({ component: "other-page", props: {}, state: {}, incomplete: false });
  }

  window.__hydrationObserver.takeRecords();
  try {
    window.__hydrated = hydratePage(document.body);
  } catch (error) {
    window.__hydrationError = String(error && error.message || error);
  }
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;

</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require("fs");
const path = require("path");
const http = require("http");
const { chromium } = require("playwright");
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const requestPath = req.url.split("?")[0];
  const name = requestPath === "/" ? "index.html" : requestPath.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {"content-type": name.endsWith(".js") ? "text/javascript" : "text/html"}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.on("pageerror", error => console.error(error.stack || error));
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    await page.waitForFunction(() => window.__hydrated && window.__resolveRequest);
    const initial = await page.evaluate(() => ({ mutations: window.__initialMutations, pending: !!document.querySelector(".pending"), same: document.querySelector("main") === window.__rootBefore }));
    if (initial.mutations !== 0 || !initial.pending || !initial.same) throw new Error(JSON.stringify(initial));
    await page.evaluate(() => window.__resolveRequest("ready"));
    await page.waitForFunction(() => document.querySelector(".done"));
    const settled = await page.evaluate(() => ({ pending: !!document.querySelector(".pending"), done: document.querySelector(".done")?.textContent, title: document.querySelector(".done")?.getAttribute("title"), same: document.querySelector("main") === window.__rootBefore }));
    if (settled.pending || settled.done !== "ready" || settled.title !== "ready" || !settled.same) throw new Error(JSON.stringify(settled));
    const draft = await page.evaluate(() => ({ value: document.querySelector(".draft")?.value, output: document.querySelector(".draft-output")?.textContent }));
    if (draft.value !== "server" || draft.output !== "server") throw new Error(JSON.stringify(draft));
    await page.fill(".draft", "typed");
    await page.waitForFunction(() => document.querySelector(".draft-output")?.textContent === "typed");
    if (await page.locator(".branch").textContent() !== "Off") throw new Error("nested await if did not mount its initial branch");
    await page.click(".toggle");
    await page.waitForFunction(() => document.querySelector(".branch")?.textContent === "On");
    if (await page.locator(".row").count() !== 1 || await page.locator(".row").textContent() !== "A") throw new Error("nested await each did not mount its initial row");
    await page.click(".add");
    await page.waitForFunction(() => document.querySelectorAll(".row").length === 2);
    const keyedBefore = await page.evaluate(() => { const row = document.querySelector(".keyed-row"); row.focus(); window.__keyedA = row; return row.textContent; });
    if (keyedBefore !== "A") throw new Error("keyed each initial row missing");
    await page.evaluate(() => document.querySelector(".reorder").dispatchEvent(new MouseEvent("click", { bubbles: true })));
    await page.waitForFunction(() => document.querySelectorAll(".keyed-row")[0]?.textContent === "B");
    const keyedAfter = await page.evaluate(() => ({ same: document.querySelectorAll(".keyed-row")[1] === window.__keyedA, focused: document.activeElement === window.__keyedA }));
    if (!keyedAfter.same || !keyedAfter.focused) throw new Error(JSON.stringify(keyedAfter));
    await page.click(".done");
    const saved = await page.evaluate(() => window.__saved);
    if (saved !== 1) throw new Error("await branch event did not run: " + saved);
    await page.evaluate(() => { window.__doneBeforeRestart = document.querySelector(".done"); window.__restartRequest(); });
    await page.waitForFunction(() => document.querySelector(".pending"));
    await page.evaluate(() => window.__doneBeforeRestart.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    const restarted = await page.evaluate(() => ({ oldDoneConnected: window.__doneBeforeRestart.isConnected, saved: window.__saved }));
    if (restarted.oldDoneConnected || restarted.saved !== 1) throw new Error("await branch replacement leaked listener: " + JSON.stringify(restarted));
    await page.evaluate(() => window.__resolveRequest("again"));
    await page.waitForFunction(() => document.querySelector(".done")?.textContent === "again");

    await page.evaluate(() => {
      window.__doneBeforeDestroy = document.querySelector(".done");
      window.__hydrated.component.destroy();
      window.__doneBeforeDestroy.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const destroyed = await page.evaluate(() => ({ rootConnected: window.__rootBefore.isConnected, doneConnected: window.__doneBeforeDestroy.isConnected, saved: window.__saved }));
    await page.goto("http://127.0.0.1:" + server.address().port + "/?mismatch=1");
    await page.waitForFunction(() => typeof window.__hydrationError === "string");
    const mismatch = await page.evaluate(() => ({ error: window.__hydrationError, mutations: window.__initialMutations, hydrated: !!window.__hydrated, tag: document.querySelector("section")?.tagName }));
    if (!mismatch.error.includes("Vela hydration mismatch [vela-page]: element range e0 expected <main> but found <section>") || mismatch.mutations !== 0 || mismatch.hydrated || mismatch.tag !== "SECTION") throw new Error("unexpected hydration mismatch result: " + JSON.stringify(mismatch));

    if (destroyed.rootConnected || destroyed.doneConnected || destroyed.saved !== 1) throw new Error("hydration destroy leaked DOM or listener: " + JSON.stringify(destroyed));

    await page.goto("http://127.0.0.1:" + server.address().port + "/?payload=1");
    await page.waitForFunction(() => typeof window.__hydrationError === "string");
    const payloadMismatch = await page.evaluate(() => ({ error: window.__hydrationError, mutations: window.__initialMutations, hydrated: !!window.__hydrated, pending: !!document.querySelector(".pending") }));
    if (!payloadMismatch.error.includes("Vela hydration mismatch [vela-page]: state payload belongs to another component") || payloadMismatch.mutations !== 0 || payloadMismatch.hydrated || !payloadMismatch.pending) throw new Error("unexpected payload mismatch result: " + JSON.stringify(payloadMismatch));

    await page.goto("http://127.0.0.1:" + server.address().port + "/?reject=1");
    await page.waitForFunction(() => window.__hydrated && window.__rejectRequest);
    const rejectedInitial = await page.evaluate(() => ({ mutations: window.__initialMutations, pending: !!document.querySelector(".pending"), same: document.querySelector("main") === window.__rootBefore }));
    if (rejectedInitial.mutations !== 0 || !rejectedInitial.pending || !rejectedInitial.same) throw new Error(JSON.stringify(rejectedInitial));
    await page.evaluate(() => window.__rejectRequest("broken"));
    await page.waitForFunction(() => document.querySelector(".failed")?.textContent === "broken");
    const rejected = await page.evaluate(() => ({ pending: !!document.querySelector(".pending"), failed: document.querySelector(".failed")?.textContent, same: document.querySelector("main") === window.__rootBefore }));
    if (rejected.pending || rejected.failed !== "broken" || !rejected.same) throw new Error(JSON.stringify(rejected));
    process.stdout.write("OK");
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});
`
	run := exec.Command(node, "-e", browserScript, dir)
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("browser await hydration fixture failed: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "OK" {
		t.Fatalf("browser await hydration fixture output=%q", output)
	}
}

func TestBrowserSSRHydrationSelectBindingAdoptsLiveValue(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let choice="a";</script><template><main><select class="choice" bind:value={choice}><option value="a">A</option><option value="b">B</option></select><p class="output">{choice}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-page:0--><main vela-page=""><!--e:vela-page:1--><select vela-page="" class="choice"><!--e:vela-page:2--><option vela-page="" value="a" selected>A</option><!--/e:vela-page:2--><!--e:vela-page:3--><option vela-page="" value="b">B</option><!--/e:vela-page:3--></select><!--/e:vela-page:1--><!--e:vela-page:4--><p vela-page="" class="output"><!--v:vela-page:0-->a<!--/v:vela-page:0--></p><!--/e:vela-page:4--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Choice":"a"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from "./Page.hydrate.js";
  if (new URLSearchParams(location.search).has("pre")) document.querySelector(".choice").value = "b";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydratePage(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require("fs");
const path = require("path");
const http = require("http");
const { chromium } = require("playwright");
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const requestPath = req.url.split("?")[0];
  const name = requestPath === "/" ? "index.html" : requestPath.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {"content-type": name.endsWith(".js") ? "text/javascript" : "text/html"}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => { window.__choice = document.querySelector(".choice"); });
    await page.selectOption(".choice", "b");
    await page.waitForFunction(() => document.querySelector(".output")?.textContent === "b");
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, same: document.querySelector(".choice") === window.__choice, value: document.querySelector(".choice")?.value, output: document.querySelector(".output")?.textContent }));
    if (result.initial !== 0 || !result.same || result.value !== "b" || result.output !== "b") throw new Error(JSON.stringify(result));
    await page.goto("http://127.0.0.1:" + server.address().port + "/?pre=1");
    await page.waitForFunction(() => window.__hydrated && document.querySelector(".output")?.textContent === "b");
    const pre = await page.evaluate(() => ({ value: document.querySelector(".choice")?.value, output: document.querySelector(".output")?.textContent }));
    if (pre.value !== "b" || pre.output !== "b") throw new Error(JSON.stringify(pre));
    process.stdout.write("OK");
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});
`
	run := exec.Command(node, "-e", browserScript, dir)
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("select hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("select hydration browser output=%q", got)
	}
}

func TestBrowserSSRHydrationBindingsAdoptLiveControlsThenUpdateState(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let title='server'; export let done=false; export let note='server note';</script><template><main><input class="input" bind:value={title} /><input class="check" type="checkbox" bind:checked={done} /><textarea class="note" bind:value={note} /><p class="output">{title}:{done}</p><p class="note-output">{note}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-page:0--><main vela-page=""><!--e:vela-page:1--><input vela-page="" class="input" value="server"><!--/e:vela-page:1--><!--e:vela-page:2--><input vela-page="" class="check" type="checkbox"><!--/e:vela-page:2--><!--e:vela-page:3--><textarea vela-page="" class="note">server note</textarea><!--/e:vela-page:3--><!--e:vela-page:4--><p vela-page="" class="output"><!--v:vela-page:0-->server<!--/v:vela-page:0-->:<!--v:vela-page:1-->false<!--/v:vela-page:1--></p><!--/e:vela-page:4--><!--e:vela-page:5--><p vela-page="" class="note-output"><!--v:vela-page:2-->server note<!--/v:vela-page:2--></p><!--/e:vela-page:5--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Title":"server","Done":false,"Note":"server note"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from './Page.hydrate.js';
  if (new URLSearchParams(location.search).has('pre')) document.querySelector('.input').value = 'typed-before-hydration';
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydratePage(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const requestPath = req.url.split('?')[0];
  const name = requestPath === '/' ? 'index.html' : requestPath.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    const errors = [];
    page.on('pageerror', error => errors.push(String(error.stack || error)));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    try { await page.waitForFunction(() => window.__hydrated); }
    catch (error) { throw new Error(String(error.stack || error) + '\n' + errors.join('\n')); }
    await page.evaluate(() => { window.__input = document.querySelector('.input'); window.__check = document.querySelector('.check'); window.__note = document.querySelector('.note'); });
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.locator('.input').fill('client');
    await page.waitForFunction(() => document.querySelector('.output')?.textContent === 'client:false');
    await page.locator('.check').check();
    await page.waitForFunction(() => document.querySelector('.output')?.textContent === 'client:true');
    await page.locator('.note').fill('client note');
    await page.waitForFunction(() => document.querySelector('.note-output')?.textContent === 'client note');
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, sameInput: document.querySelector('.input') === window.__input, sameCheck: document.querySelector('.check') === window.__check, sameNote: document.querySelector('.note') === window.__note, value: document.querySelector('.input')?.value, checked: document.querySelector('.check')?.checked, note: document.querySelector('.note')?.value, text: document.querySelector('.output')?.textContent, noteText: document.querySelector('.note-output')?.textContent }));
    if (result.initial !== 0 || !result.sameInput || !result.sameCheck || !result.sameNote || result.value !== 'client' || !result.checked || result.note !== 'client note' || result.text !== 'client:true' || result.noteText !== 'client note') throw new Error(JSON.stringify(result));
    await page.goto('http://127.0.0.1:' + server.address().port + '/?pre=1');
    await page.waitForFunction(() => window.__hydrated && document.querySelector('.output')?.textContent === 'typed-before-hydration:false');
    const preHydration = await page.evaluate(() => ({ value: document.querySelector('.input')?.value, text: document.querySelector('.output')?.textContent }));
    if (preHydration.value !== 'typed-before-hydration' || preHydration.text !== 'typed-before-hydration:false') throw new Error(JSON.stringify(preHydration));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser binding hydration fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser binding hydration fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationNestedTextAttributeAndEventAdoptThenUpdate(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let title='server'; export let selected=false; export let color='red'; function change() { title = 'client'; selected = !selected; color = 'blue'; }</script><template><main><section><p class="title" :title={title} class:selected={selected} style:color={color} @click={change}>{title}</p></section></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-page:0--><main vela-page=""><!--e:vela-page:1--><section vela-page=""><!--e:vela-page:2--><p vela-page="" class="title" title="server" style="color: red;"><!--v:vela-page:0-->server<!--/v:vela-page:0--></p><!--/e:vela-page:2--></section><!--/e:vela-page:1--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Title":"server","Selected":false,"Color":"red"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from './Page.hydrate.js';
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydratePage(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    const errors = [];
    page.on('pageerror', error => errors.push(String(error.stack || error)));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    try { await page.waitForFunction(() => window.__hydrated); }
    catch (error) { throw new Error(String(error.stack || error) + '\n' + errors.join('\n')); }
    await page.evaluate(() => { window.__paragraph = document.querySelector('.title'); });
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => document.querySelector('.title').click());
    await page.waitForFunction(() => document.querySelector('.title')?.textContent === 'client' && document.querySelector('.title')?.getAttribute('title') === 'client' && document.querySelector('.title')?.classList.contains('selected') && document.querySelector('.title')?.style.color === 'blue');
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, sameParagraph: document.querySelector('.title') === window.__paragraph, text: document.querySelector('.title')?.textContent, title: document.querySelector('.title')?.getAttribute('title'), selected: document.querySelector('.title')?.classList.contains('selected'), color: document.querySelector('.title')?.style.color }));
    if (result.initial !== 0 || !result.sameParagraph || result.text !== 'client' || result.title !== 'client' || !result.selected || result.color !== 'blue') throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser nested text hydration fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser nested text hydration fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationStaticKeyAdoptsThenReplacesRange(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let identity='a';</script><template><main>{#key identity}<span class="row">Row</span>{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-page:0--><main vela-page=""><!--v:vela-page:0--><!--e:vela-page:1--><span vela-page="" class="row">Row</span><!--/e:vela-page:1--><!--/v:vela-page:0--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Identity":"a"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from './Page.hydrate.js';
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydratePage(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    const errors = [];
    page.on('pageerror', error => errors.push(String(error.stack || error)));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    try { await page.waitForFunction(() => window.__hydrated); }
    catch (error) { throw new Error(String(error.stack || error) + '\n' + errors.join('\n')); }
    await page.evaluate(() => { window.__initialRow = document.querySelector('.row'); });
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => window.__hydrated.component.set({identity:'b'}));
    await page.waitForFunction(() => document.querySelector('.row') !== window.__initialRow);
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, count: document.querySelectorAll('.row').length, text: document.querySelector('.row')?.textContent }));
    if (result.initial !== 0 || result.count !== 1 || result.text !== 'Row') throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser key hydration fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser key hydration fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationNestedStaticBlocksAdoptThenReplaceRanges(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let visible=false; export let items=[];</script><template><main><section>{#if visible}<span class="state">On</span>{:else}<span class="state">Off</span>{/if}{#each items as item}<span class="row">{item}</span>{:else}<span class="empty">Empty</span>{/each}</section></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-page:0--><main vela-page=""><!--e:vela-page:1--><section vela-page=""><!--v:vela-page:0--><!--e:vela-page:3--><span vela-page="" class="state">Off</span><!--/e:vela-page:3--><!--/v:vela-page:0--><!--v:vela-page:1--><!--e:vela-page:4--><span vela-page="" class="row"><!--v:vela-page:2-->1<!--/v:vela-page:2--></span><!--/e:vela-page:4--><!--e:vela-page:4--><span vela-page="" class="row"><!--v:vela-page:2-->2<!--/v:vela-page:2--></span><!--/e:vela-page:4--><!--/v:vela-page:1--></section><!--/e:vela-page:1--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Visible":false,"Items":[1,2]},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from './Page.hydrate.js';
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydratePage(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    const errors = [];
    page.on('pageerror', error => errors.push(String(error.stack || error)));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    try { await page.waitForFunction(() => window.__hydrated); }
    catch (error) { throw new Error(String(error.stack || error) + '\\n' + errors.join('\\n')); }
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => window.__hydrated.component.set({visible:true, items:[1]}));
    await page.waitForFunction(() => document.querySelector('.state')?.textContent === 'On' && document.querySelectorAll('.row').length === 1 && document.querySelector('.row')?.textContent === '1');
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, text: document.querySelector('.state')?.textContent, stateCount: document.querySelectorAll('main section > .state').length, rowCount: document.querySelectorAll('main section > .row').length, rowText: document.querySelector('.row')?.textContent, emptyCount: document.querySelectorAll('main section > .empty').length }));
    if (result.initial !== 0 || result.text !== 'On' || result.stateCount !== 1 || result.rowCount !== 1 || result.rowText !== '1' || result.emptyCount !== 0) throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser hydration fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser hydration fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachRetainsFocusedRowAcrossDeepReconcile(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let items=$state.deep([{id:'a',label:'Alpha'},{id:'b',label:'Beta'}]); function updateRows() { items[1].label = 'Beta*'; items = [items[1], {id:'c',label:'Gamma'}]; }</script><template><main @click={updateRows}>{#each items as item (item.id)}<button class="row">{item.label}</button>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-page:0--><main vela-page=""><!--v:vela-page:0--><!--e:vela-page:1--><button vela-page="" class="row"><!--v:vela-page:2-->Alpha<!--/v:vela-page:2--></button><!--/e:vela-page:1--><!--e:vela-page:1--><button vela-page="" class="row"><!--v:vela-page:2-->Beta<!--/v:vela-page:2--></button><!--/e:vela-page:1--><!--/v:vela-page:0--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{"items":[{"id":"a","label":"Alpha"},{"id":"b","label":"Beta"}]},"deep":["items"],"incomplete":false}</script>
<script type="module">
  import hydratePage from './Page.hydrate.js';
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydratePage(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    const errors = [];
    page.on('pageerror', error => errors.push(String(error.stack || error)));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    try { await page.waitForFunction(() => window.__hydrated); }
    catch (error) { throw new Error(String(error.stack || error) + '\n' + errors.join('\n')); }
    await page.evaluate(() => {
      window.__focusedRow = document.querySelectorAll('.row')[1];
      window.__focusedRow.focus();
	  window.__wasFocused = document.activeElement === window.__focusedRow;
    });
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => document.querySelector('main').click());
    await page.waitForFunction(() => Array.from(document.querySelectorAll('.row')).map(row => row.textContent).join('|') === 'Beta*|Gamma');
    const result = await page.evaluate(() => {
      const rows = document.querySelectorAll('.row');
      return {
        initial: window.__initialMutations,
		wasFocused: window.__wasFocused,
        rowCount: rows.length,
        firstRetained: rows[0] === window.__focusedRow,
        focused: document.activeElement === window.__focusedRow,
        text: Array.from(rows).map(row => row.textContent).join('|'),
      };
    });
	if (result.initial !== 0 || !result.wasFocused || result.rowCount !== 2 || !result.firstRetained || !result.focused || result.text !== 'Beta*|Gamma') throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser keyed hydration fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser keyed hydration fixture output=%q", got)
	}
}

func TestGeneratedSSRHydrationClientAdoptsElementAndHandlesEvent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	sfc, err := parseSFC(`<script>export let title='server'; export let selected=false; function toggle() { selected = !selected; }</script><template><button class:selected={selected} @click={toggle}>{title}</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	clientSource := PrintJS(client, PrintPretty)
	for _, forbidden := range []string{
		"document.createElement('button')",
		"appendChild($$root)",
		"$$root.setAttribute('vela-page', '')",
		"$$root.classList.toggle('selected', selected);",
	} {
		if strings.Contains(clientSource, forbidden) {
			t.Fatalf("hydration client unexpectedly writes initial DOM through %q:\n%s", forbidden, clientSource)
		}
	}
	if !strings.Contains(clientSource, "$$acquireHydrationElement($$walker, 0, 'button')") {
		t.Fatalf("hydration client does not acquire existing root element:\n%s", clientSource)
	}
	if !strings.Contains(clientSource, "$$acquireHydrationRange($$walker, 0)") {
		t.Fatalf("hydration client does not acquire existing text marker range:\n%s", clientSource)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"package.json":           []byte(`{"type":"module"}`),
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(clientSource),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const runner = `import hydratePage from './Page.hydrate.js';
function assert(value, message) { if (!value) throw new Error(message); }
function marker(data) { return { data }; }
const listeners = new Map();
const toggles = [];
const element = {
  nodeType: 1, tagName: 'BUTTON',
  addEventListener(name, handler) { listeners.set(name, handler); },
  removeEventListener(name) { listeners.delete(name); },
  classList: { toggle(name, value) { toggles.push([name, value]); } },
  remove() {},
};
const start = marker('e:vela-page:0');
const end = marker('/e:vela-page:0');
const textStart = marker('v:vela-page:0');
const text = { nodeType: 3, data: 'server' };
const textEnd = marker('/v:vela-page:0');
start.nextSibling = element;
element.nextSibling = end;
textStart.nextSibling = text;
text.nextSibling = textEnd;
const comments = [start, textStart, textEnd, end];
const payload = { type: 'application/json', textContent: '{"component":"vela-page","props":{"Title":"server","Selected":false},"state":{},"incomplete":false}' };
const document = {
  createTreeWalker() { let index = 0; return { nextNode() { return index < comments.length ? comments[index++] : null; } }; },
  getElementById(id) { return id === 'vela-state-vela-page' ? payload : null; },
};
globalThis.document = document;
const root = { ownerDocument: document };
const adopted = hydratePage(root);
assert(adopted.component.el === element, 'factory must retain existing element');
assert(listeners.has('click'), 'factory must attach click listener');
listeners.get('click')({ target: element });
await Promise.resolve();
assert(toggles.length === 1 && toggles[0][0] === 'selected' && toggles[0][1] === true, 'click must update only the dynamic class');
adopted.component.set({ title: 'client' });
await Promise.resolve();
assert(text.data === 'client', 'prop update must write only the adopted text range');
console.log('OK');
`
	if err := os.WriteFile(filepath.Join(dir, "run.mjs"), []byte(runner), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "run.mjs")
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hydration client runner: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("hydration client output=%q", got)
	}
}

func TestSSRHydrationClientAdoptsLocalChildComponent(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		export let title = "server";
	</script><template><main><Child :title={title} /></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`import { Child } from './Child.hydrate.js';`,
		`Child($$root).component`,
		`.set({ title: title })`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("child hydration lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, `createComment("comp")`) {
		t.Fatalf("child hydration must not create a CSR component anchor:\n%s", source)
	}
}

func TestSSRHydrationStructuralChildImportsKeepCSRFactoryDistinct(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		let visible = $state(true);
	</script><template><main>{#if visible}<Child />{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	aliases := ssrHydrationStructuralComponentAliases(sfc.TemplateAST.Nodes)
	if got, want := aliases["Child"], "$$csr_Child"; got != want {
		t.Fatalf("CSR child alias=%q, want %q", got, want)
	}
	g := &ASTGen{
		analysis:               analyzeScriptAST(sfc.ScriptAST),
		domAcquireMode:         DOMAcquireHydrate,
		hydrationComponents:    map[string]bool{"Child": true},
		hydrationCSRComponents: aliases,
	}
	source := PrintJS(&JsProgram{Body: g.buildImportDecls()}, PrintPretty)
	for _, want := range []string{
		"import { Child } from \x27./Child.hydrate.js\x27;",
		"import { Child as $$csr_Child } from \x27./Child.js\x27;",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("structural child imports miss %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationIfChildAdoptsThenRemountsWithCSRFactory(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		export let visible = true;
		export let title = "server";
	</script><template><main>{#if visible}<Child :title={title} />{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"import { Child } from \x27./Child.hydrate.js\x27;",
		"import { Child as $$csr_Child } from \x27./Child.js\x27;",
		"let $$hif0_component = null;",
		"$$hif0_component = Child($$root).component;",
		"$$hif0_component.set({ title: title });",
		"$$hif0_component = $$csr_Child($$frag, { title: title }, {});",
		"$$hif0_component.destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("structural child lifecycle misses %q:\\n%s", want, source)
		}
	}
	destroyAt := strings.Index(source, "$$hif0_component.destroy();")
	removeAt := strings.Index(source, "$$node.remove();")
	if destroyAt < 0 || removeAt < 0 || destroyAt > removeAt {
		t.Fatalf("structural child must destroy before removing its SSR range:\\n%s", source)
	}
}

func TestSSRHydrationIfChildRemountsStaticSiblingsInTemplateOrder(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		export let visible = true;
		export let title = "server";
	</script><template><main>{#if visible}<p class="before">Before</p><Child :title={title} /><p class="after">After</p>{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static siblings around direct #if child must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	before := strings.Index(source, "Before")
	mount := strings.Index(source, "$$hif0_component = $$csr_Child")
	after := strings.Index(source, "After")
	if before < 0 || mount < 0 || after < 0 || !(before < mount && mount < after) {
		t.Fatalf("static sibling remount order is not template order:\n%s", source)
	}
}

func TestSSRHydrationIfBranchOwnsMultipleDistinctChildHandles(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Alpha } from "./Alpha.vela";
		import { Beta } from "./Beta.vela";
		export let visible = true;
		export let title = "server";
	</script><template><main>{#if visible}<p>Before</p><Alpha :title={title} /><Beta :title={title} /><p>After</p>{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("multiple distinct direct #if children must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$hif0_component_0 = null;",
		"let $$hif0_component_1 = null;",
		"$$hif0_component_0 = Alpha($$root).component;",
		"$$hif0_component_1 = Beta($$root).component;",
		"$$hif0_component_0 = $$csr_Alpha($$frag, { title: title }, {});",
		"$$hif0_component_1 = $$csr_Beta($$frag, { title: title }, {});",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("multi-child lifecycle misses %q:\n%s", want, source)
		}
	}
	if first, second := strings.Index(source, "$$hif0_component_1.destroy();"), strings.Index(source, "$$hif0_component_0.destroy();"); first < 0 || second < 0 || first > second {
		t.Fatalf("multi-child handles must destroy in reverse order:\n%s", source)
	}
}

func TestSSRHydrationIfBranchNamespacesRepeatedChildBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let visible = true;</script><template><main>{#if visible}<Child /><Child />{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("repeated structural child bindings have static path-scoped payload namespaces")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$instanceID = $$hydrate.instanceID || 'vela-parent';",
		"Child($$root, null, $$instanceID + '--n0--n0--if0--n0--component-Child').component",
		"Child($$root, null, $$instanceID + '--n0--n0--if0--n1--component-Child').component",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("repeated child hydration misses %q:\\n%s", want, source)
		}
	}
}

func TestSSRHydrationUnkeyedEachAdoptsAndRebuildsDirectChildRows(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = []; </script><template><main>{#each rows as row, index}<Child :value={row.value} :index={index} />{:else}<p>Empty</p>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("unkeyed direct child rows must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$heach0_components = [];",
		"Child($$root, null, $$instanceID + '--each-",
		"$$heach0_components.push(Child($$root, null,",
		"$$csr_Child($$frag, { value: $$item.value, index: $$heach0_index }, {});",
		"$$component.destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("unkeyed child each lifecycle misses %q:\\n%s", want, source)
		}
	}
}

func TestSSRHydrationUnkeyedEachAdoptsAndRebuildsChildChains(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Alpha } from "./Alpha.vela"; import { Beta } from "./Beta.vela"; export let rows = [];</script><template><main>{#each rows as row}<p>before</p><Alpha :value={row.value} /><Beta :value={row.value} /><p>after</p>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("unkeyed child chains must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"Alpha($$root, null, $$instanceID + '--each-", "Beta($$root, null, $$instanceID + '--each-",
		"$$csr_Alpha($$frag, { value: $$item.value }, {});", "$$csr_Beta($$frag, { value: $$item.value }, {});",
		"before</p>", "after</p>",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("unkeyed child chain misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyedEachRetainsDirectChildInstances(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = []; </script><template><main>{#each rows as row (row.id)}<Child :value={row.value} />{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("keyed direct child rows must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$heach0_components = new Map();",
		"const $$components = [];",
		"$$components.push(Child($$root, null, $$instanceID + '--each-",
		"$$heach0_components.set($$key, $$components);",
		"$$heach0_components.get($$heach0_key($$item, $$index))",
		"$$components[0].set({ value: $$item.value });",
		"for (const $$component of $$components.slice().reverse())",
		"$$heach0_components.delete($$key);",
		"$$csr_Child($$frag, { value: $$item.value }, {});",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("keyed child each lifecycle misses %q:\\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyedEachOwnsChildChains(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Alpha } from "./Alpha.vela"; import { Beta } from "./Beta.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}<p>before</p><Alpha :value={row.value} /><Beta :value={row.value} /><p>after</p>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("keyed child chains must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$component_0 = $$csr_Alpha", "$$component_1 = $$csr_Beta",
		"before</p>", "after</p>",
		"$$components[0].set({ value: $$item.value });", "$$components[1].set({ value: $$item.value });",
		"$$components.push(Alpha($$root, null, $$instanceID + '--each-", "$$components.push(Beta($$root, null, $$instanceID + '--each-",
		"$$components.slice().reverse()",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("keyed child chain misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyBlockAdoptsAndRemountsDirectChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let version = "a"; export let value = "server";</script><template><main>{#key version}<Child :value={value} />{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("direct child in #key must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$hkey0_component = null;",
		"Child($$root, null, $$instanceID + '--key-",
		"$$hkey0_component.set({ value: value });",
		"$$hkey0_component.destroy();",
		"$$csr_Child($$frag, { value: value }, {});",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("key child lifecycle misses %q:\\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyBlockOwnsChildChainInTemplateOrder(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Alpha } from "./Alpha.vela"; import { Beta } from "./Beta.vela"; export let version = "a"; export let title = "server";</script><template><main>{#key version}<p>before</p><Alpha :title={title} /><Beta :title={title} /><p>after</p>{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("#key child chain with static siblings must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$hkey0_component_0 = null;",
		"let $$hkey0_component_1 = null;",
		"Alpha($$root, null, $$instanceID + '--key-",
		"Beta($$root, null, $$instanceID + '--key-",
		"$$hkey0_component_0.set({ title: title });",
		"$$hkey0_component_1.set({ title: title });",
		"$$hkey0_component_1.destroy();",
		"$$hkey0_component_0.destroy();",
		"$$csr_Alpha($$frag, { title: title }, {});",
		"$$csr_Beta($$frag, { title: title }, {});",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("key child chain lifecycle misses %q:\n%s", want, source)
		}
	}
	before := strings.Index(source, "before</p>")
	alpha := strings.Index(source, "$$csr_Alpha($$frag")
	beta := strings.Index(source, "$$csr_Beta($$frag")
	after := strings.Index(source, "after</p>")
	if before < 0 || alpha < before || beta < alpha || after < beta {
		t.Fatalf("key child chain does not preserve static/component source order:\n%s", source)
	}
}

func TestBrowserSSRHydrationKeyBlockReplacesChildChain(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let id = ""; export let value = "";</script><template><p class="child" :data-id={id}>{id}:{value}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let version = "a"; export let value = "server";</script><template><main>{#key version}<p class="before">before</p><Child :id={"a"} :value={value} /><Child :id={"b"} :value={value} /><p class="after">after</p>{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	block := main.Children[0].(*IfChainNode)
	components := []*ComponentNode{block.Branches[0].Children[1].(*ComponentNode), block.Branches[0].Children[2].(*ComponentNode)}
	keyID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).ifID(block.Span)
	if !ok {
		t.Fatal("parent key marker is missing")
	}
	childScope := func(component *ComponentNode) string {
		return "vela-parent--key-" + itoa(block.Span.Start) + "-a--" + ssrRuntimeComponentSuffix(parentPlan.ComponentInstanceIdentities[component.Span])
	}
	childHTML := func(component *ComponentNode, id string) string {
		scope := childScope(component)
		return "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\" data-id=\"" + id + "\"><!--v:" + scope + ":0-->" + id + "<!--/v:" + scope + ":0-->:<!--v:" + scope + ":1-->server<!--/v:" + scope + ":1--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Id\":\"" + id + "\",\"Value\":\"server\"},\"state\":{},\"incomplete\":false}</script>"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(keyID) + "--><p vela-parent=\"\" class=\"before\">before</p>" + childHTML(components[0], "a") + childHTML(components[1], "b") + "<p vela-parent=\"\" class=\"after\">after</p><!--/v:vela-parent:" + itoa(keyID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Version\":\"a\",\"Value\":\"server\"},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js'; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(serverHTML),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require('fs'),path=require('path'),http=require('http'),{chromium}=require('playwright');const root=process.argv[1];const server=http.createServer((req,res)=>{const name=req.url==='/'?'index.html':req.url.slice(1);try{res.writeHead(200,{'content-type':name.endsWith('.js')?'text/javascript':'text/html'}).end(fs.readFileSync(path.join(root,name)))}catch{res.writeHead(404).end()}});server.listen(0,'127.0.0.1',async()=>{let browser;try{browser=await chromium.launch({headless:true});const page=await browser.newPage();page.setDefaultTimeout(5000);const errors=[];page.on('pageerror',error=>errors.push(String(error.stack||error)));await page.goto('http://127.0.0.1:'+server.address().port+'/');try{await page.waitForFunction(()=>window.__hydrated)}catch(error){throw new Error(String(error.stack||error)+'\\n'+errors.join('\\n'))}await page.evaluate(()=>{window.__a=document.querySelector('[data-id="a"]');window.__b=document.querySelector('[data-id="b"]');window.__hydrated.component.set({value:'client'})});await page.waitForTimeout(40);await page.evaluate(()=>{window.__retained={initial:window.__initialMutations,a:window.__a===document.querySelector('[data-id="a"]'),b:window.__b===document.querySelector('[data-id="b"]')}});await page.evaluate(()=>window.__hydrated.component.set({version:'b'}));await page.waitForTimeout(40);const result=await page.evaluate(()=>({initial:window.__retained.initial,retainedA:window.__retained.a,retainedB:window.__retained.b,before:document.querySelectorAll('.before').length,after:document.querySelectorAll('.after').length,rows:Array.from(document.querySelectorAll('.child')).map(n=>n.textContent).join('|')}));if(result.initial!==0||!result.retainedA||!result.retainedB||result.before!==1||result.after!==1||result.rows!=='a:client|b:client')throw new Error(JSON.stringify(result));process.stdout.write('OK')}catch(error){console.error(error&&error.stack||error);process.exitCode=1}finally{await browser?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("key child chain browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("key child chain browser fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachRetainsDirectChildInstances(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let id = ""; export let value = "";</script><template><p class="child" :data-id={id}>{value}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}<Child :id={row.id} :value={row.value} />{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	block := main.Children[0].(*EachBlockNode)
	component := block.Children[0].(*ComponentNode)
	identity := parentPlan.ComponentInstanceIdentities[component.Span]
	suffix := ssrRuntimeComponentSuffix(identity)
	eachID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).eachID(block.Span)
	if !ok {
		t.Fatal("parent each marker is missing")
	}
	childScope := func(key string) string {
		return "vela-parent--each-" + itoa(block.Span.Start) + "-" + key + "--" + suffix
	}
	rowScope := func(key string) string {
		return "vela-parent--each-" + itoa(block.Span.Start) + "-" + key
	}
	childHTML := func(key, value string) string {
		scope := childScope(key)
		return "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\" data-id=\"" + key + "\"><!--v:" + scope + ":0-->" + value + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Id\":\"" + key + "\",\"Value\":\"" + value + "\"},\"state\":{},\"incomplete\":false}</script>"
	}
	rowHTML := func(key, value string) string {
		return "<!--r:" + rowScope(key) + ":" + itoa(eachID) + "-->" + childHTML(key, value) + "<!--/r:" + rowScope(key) + ":" + itoa(eachID) + "-->"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML("a", "Alpha") + rowHTML("b", "Beta") + "<!--/v:vela-parent:" + itoa(eachID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"id\":\"a\",\"value\":\"Alpha\"},{\"id\":\"b\",\"value\":\"Beta\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js'; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(serverHTML),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => {
      window.__a = document.querySelector('.child[data-id="a"]');
      window.__b = document.querySelector('.child[data-id="b"]');
      window.__hydrated.component.set({rows:[{id:'b',value:'Beta*'},{id:'a',value:'Alpha*'},{id:'c',value:'Gamma'}]});
    });
    await page.waitForFunction(() => Array.from(document.querySelectorAll('.child')).map(node => node.dataset.id + ':' + node.textContent).join('|') === 'b:Beta*|a:Alpha*|c:Gamma');
    await page.evaluate(() => { window.__retained = {
      initial: window.__initialMutations,
      a: document.querySelector('.child[data-id="a"]') === window.__a,
      b: document.querySelector('.child[data-id="b"]') === window.__b,
    }; });
    await page.evaluate(() => window.__hydrated.component.set({rows:[{id:'b',value:'Beta final'}]}));
    await page.waitForFunction(() => document.querySelectorAll('.child').length === 1 && document.querySelector('.child')?.textContent === 'Beta final');
    const result = await page.evaluate(() => ({
      initial: window.__retained.initial,
      a: window.__retained.a,
      b: window.__retained.b,
      payloadA: !!document.getElementById('vela-state-' + window.__aScope),
      payloadB: !!document.getElementById('vela-state-' + window.__bScope),
    }));
    if (result.initial !== 0 || !result.a || !result.b || result.payloadA || !result.payloadB) throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	// Pass the actual server namespaces through globals instead of duplicating
	// their structural-path recipe inside the browser script.
	serverHTML = strings.Replace(serverHTML, "window.__initialMutations", "window.__aScope='"+childScope("a")+"'; window.__bScope='"+childScope("b")+"'; window.__initialMutations", 1)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(serverHTML), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed child browser fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachRetainsChildChains(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let id = ""; export let value = "";</script><template><p class="child">{value}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}<p class="before">before</p><Child :id={row.id + "-a"} :value={row.value} /><Child :id={row.id + "-b"} :value={row.value} /><p class="after">after</p>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	block := main.Children[0].(*EachBlockNode)
	first := block.Children[1].(*ComponentNode)
	second := block.Children[2].(*ComponentNode)
	eachID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).eachID(block.Span)
	if !ok {
		t.Fatal("parent each marker is missing")
	}
	rowScope := func(key string) string {
		return "vela-parent--each-" + itoa(block.Span.Start) + "-" + key
	}
	childScope := func(key string, component *ComponentNode) string {
		identity := parentPlan.ComponentInstanceIdentities[component.Span]
		return rowScope(key) + "--" + ssrRuntimeComponentSuffix(identity)
	}
	childHTML := func(key, label, value string, component *ComponentNode) string {
		id := key + "-" + label
		scope := childScope(key, component)
		return "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + scope + ":0-->" + value + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Id\":\"" + id + "\",\"Value\":\"" + value + "\"},\"state\":{},\"incomplete\":false}</script>"
	}
	rowHTML := func(key, value string) string {
		scope := rowScope(key)
		return "<!--r:" + scope + ":" + itoa(eachID) + "--><p vela-parent=\"\" class=\"before\">before</p>" + childHTML(key, "a", value, first) + childHTML(key, "b", value, second) + "<p vela-parent=\"\" class=\"after\">after</p><!--/r:" + scope + ":" + itoa(eachID) + "-->"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML("a", "Alpha") + rowHTML("b", "Beta") + "<!--/v:vela-parent:" + itoa(eachID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"id\":\"a\",\"value\":\"Alpha\"},{\"id\":\"b\",\"value\":\"Beta\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js'; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__aFirstScope='" + childScope("a", first) + "'; window.__aSecondScope='" + childScope("a", second) + "'; window.__bFirstScope='" + childScope("b", first) + "'; window.__bSecondScope='" + childScope("b", second) + "'; window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(serverHTML),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => {
      const initial = document.querySelectorAll('.child');
      window.__aA = initial[0];
      window.__aB = initial[1];
      window.__bA = initial[2];
      window.__bB = initial[3];
      window.__hydrated.component.set({rows:[{id:'b',value:'Beta*'},{id:'a',value:'Alpha*'},{id:'c',value:'Gamma'}]});
    });
    await page.waitForFunction(() => Array.from(document.querySelectorAll('.child')).map(node => node.textContent).join('|') === 'Beta*|Beta*|Alpha*|Alpha*|Gamma|Gamma');
    await page.evaluate(() => { window.__retained = {
      initial: window.__initialMutations,
      aA: document.querySelectorAll('.child')[2] === window.__aA,
      aB: document.querySelectorAll('.child')[3] === window.__aB,
      bA: document.querySelectorAll('.child')[0] === window.__bA,
      bB: document.querySelectorAll('.child')[1] === window.__bB,
      before: document.querySelectorAll('.before').length,
      after: document.querySelectorAll('.after').length,
    }; });
    await page.evaluate(() => window.__hydrated.component.set({rows:[{id:'b',value:'Beta final'}]}));
    await page.waitForFunction(() => Array.from(document.querySelectorAll('.child')).map(node => node.textContent).join('|') === 'Beta final|Beta final');
    const result = await page.evaluate(() => ({
      ...window.__retained,
      finalBefore: document.querySelectorAll('.before').length,
      finalAfter: document.querySelectorAll('.after').length,
      payloadAFirst: !!document.getElementById('vela-state-' + window.__aFirstScope),
      payloadASecond: !!document.getElementById('vela-state-' + window.__aSecondScope),
      payloadBFirst: !!document.getElementById('vela-state-' + window.__bFirstScope),
      payloadBSecond: !!document.getElementById('vela-state-' + window.__bSecondScope),
    }));
    if (result.initial !== 0 || !result.aA || !result.aB || !result.bA || !result.bB || result.before !== 3 || result.after !== 3 || result.finalBefore !== 1 || result.finalAfter !== 1 || result.payloadAFirst || result.payloadASecond || !result.payloadBFirst || !result.payloadBSecond) throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed child chain browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed child chain browser fixture output=%q", got)
	}
}

func TestSSRProjectHydrationClientImportsLocalChildEntry(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<script>export let title = "";</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child :title={title} /></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	client, err := os.ReadFile(layout.hydrationClientPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Child.hydrate.js`,
		`Child($$root).component`,
	} {
		if !strings.Contains(string(client), want) {
			t.Fatalf("parent hydration client misses %q:\n%s", want, client)
		}
	}
	childEntry, err := os.ReadFile(layout.hydratePath(child, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(childEntry), `export function Child($$root, $$overrides, $$instanceID)`) {
		t.Fatalf("child hydrate entry misses component alias:\n%s", childEntry)
	}
}

func TestSSRProjectBuildsParentHydrationClientForSupportedStructuralChild(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<script>export let visible = false; export let title = "";</script><template><p>{#if visible}<span>{title}</span>{/if}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child :title={title} /></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	if _, err := os.Stat(layout.hydrationClientPath(parent, srcRoot)); err != nil {
		t.Fatalf("parent hydration client for supported structural child: %v", err)
	}
	entry, err := os.ReadFile(layout.hydratePath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), `.hydrate_client.js`) {
		t.Fatalf("parent hydrate entry must import the child hydration client:\n%s", entry)
	}
}

func TestBrowserSSRHydrationAdoptsLocalChildThenUpdatesProps(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let title = "";</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child :title={title} /></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--e:vela-child:0--><p vela-child="" class="child"><!--v:vela-child:0-->server<!--/v:vela-child:0--></p><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{"Title":"server"},"state":{},"incomplete":false}</script></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Title":"server"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydrateParent from "./Parent.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydrateParent(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require("fs");
const path = require("path");
const http = require("http");
const { chromium } = require("playwright");
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === "/" ? "index.html" : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {"content-type": name.endsWith(".js") ? "text/javascript" : "text/html"}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => { window.__child = document.querySelector(".child"); window.__hydrated.component.set({ title: "client" }); });
    await page.waitForFunction(() => document.querySelector(".child")?.textContent === "client");
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, same: document.querySelector(".child") === window.__child, text: document.querySelector(".child")?.textContent }));
    if (result.initial !== 0 || !result.same || result.text !== "client") throw new Error(JSON.stringify(result));
    process.stdout.write("OK");
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child hydration browser fixture: %v\n%s\nparent client:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("child hydration browser output=%q", got)
	}
}

func TestBrowserSSRHydrationIfChildAdoptsDestroysAndCSRRemounts(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>
		import { onDestroy } from "vela";
		export let title = "";
		onDestroy(() => { window.__childDestroyCount = (window.__childDestroyCount || 0) + 1; });
	</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, analyzeScriptAST(childSFC.ScriptAST), "vela-child", false, DOMAcquireCreate)

	parentSFC, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		export let visible = true;
		export let title = "server";
	</script><template><main>{#if visible}<p class="before">Before</p><Child :title={title} /><p class="after">After</p>{:else}<p class="hidden">Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--v:vela-parent:0--><p vela-parent="" class="before">Before</p><!--e:vela-child:0--><p vela-child="" class="child"><!--v:vela-child:0-->server<!--/v:vela-child:0--></p><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{"Title":"server"},"state":{},"incomplete":false}</script><p vela-parent="" class="after">After</p><!--/v:vela-parent:0--></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Visible":true,"Title":"server"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydrateParent from './Parent.hydrate.js';
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydrateParent(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => { window.__firstChild = document.querySelector('.child'); window.__hydrated.component.set({title:'client'}); });
    await page.waitForFunction(() => document.querySelector('.child')?.textContent === 'client');
    await page.evaluate(() => window.__hydrated.component.set({visible:false}));
    await page.waitForFunction(() => document.querySelector('.hidden'));
    await page.evaluate(() => { window.__payloadRemoved = !document.getElementById('vela-state-vela-child'); window.__hydrated.component.set({visible:true}); });
    await page.waitForFunction(() => document.querySelector('.child')?.textContent === 'client' && document.querySelector('.before') && document.querySelector('.after'));
    await page.evaluate(() => window.__hydrated.component.set({title:'remounted'}));
    await page.waitForFunction(() => document.querySelector('.child')?.textContent === 'remounted');
    const result = await page.evaluate(() => ({
      initial: window.__initialMutations,
      firstDetached: !window.__firstChild.isConnected,
      remounted: document.querySelector('.child') !== window.__firstChild,
      payloadRemoved: window.__payloadRemoved,
      destroyed: window.__childDestroyCount,
      text: document.querySelector('.child')?.textContent,
      order: Array.from(document.querySelector('main').children).map((node) => node.className).join(','),
    }));
    if (result.initial !== 0 || !result.firstDetached || !result.remounted || !result.payloadRemoved || result.destroyed !== 1 || result.text !== 'remounted' || result.order !== 'before,child,after') throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("structural child hydration browser fixture: %v\\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("structural child hydration browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAwaitChildAdoptsSettlesAndCleansPayload(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__awaitChildDestroys = (window.__awaitChildDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let title = "server";</script><template><main>{#await request}<p class="before">Waiting</p><Child :title={title} /><p class="after">Soon</p>{:then value}<Child :title={value} /><p class="settled">Done</p>{:catch error}<Child :title={error} /><p class="failed">Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	pending := await.PendingChildren[1].(*ComponentNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(await.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	childScope := "vela-parent--" + parentPlan.ComponentInstanceSuffixes[pending.Span]
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><p vela-parent=\"\" class=\"before\">Waiting</p><!--e:" + childScope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + childScope + ":0-->server<!--/v:" + childScope + ":0--></p><!--/e:" + childScope + ":0--><script type=\"application/json\" id=\"vela-state-" + childScope + "\">{\"component\":\"" + childScope + "\",\"props\":{\"Title\":\"server\"},\"state\":{},\"incomplete\":false}</script><p vela-parent=\"\" class=\"after\">Soon</p><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Title\":\"server\"},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js'; window.__request = new Promise(resolve => { window.__resolveRequest = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(serverHTML),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	browserScript := `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => { window.__pendingChild = document.querySelector('.child'); window.__hydrated.component.set({title:'updated'}); });
    await page.waitForFunction(() => document.querySelector('.child')?.textContent === 'updated');
    await page.evaluate(() => window.__resolveRequest('settled'));
    await page.waitForFunction(() => document.querySelector('.child')?.textContent === 'settled' && document.querySelector('.settled'));
    const result = await page.evaluate(() => ({
      initial: window.__initialMutations,
      pendingDetached: !window.__pendingChild.isConnected,
      remounted: document.querySelector('.child') !== window.__pendingChild,
      payloadRemoved: !document.getElementById('vela-state-__VELA_SCOPE__'),
      destroys: window.__awaitChildDestroys,
      before: !!document.querySelector('.before'),
      after: !!document.querySelector('.after'),
      settled: !!document.querySelector('.settled'),
      text: document.querySelector('.child')?.textContent,
    }));
    if (result.initial !== 0 || !result.pendingDetached || !result.remounted || !result.payloadRemoved || result.destroys !== 1 || result.before || result.after || !result.settled || result.text !== 'settled') throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	browserScript = strings.Replace(browserScript, "__VELA_SCOPE__", childScope, 1)
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("await child hydration browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("await child hydration browser output=%q", got)
	}
}

func TestBrowserSSRHydrationPendingAwaitNestedIfAdoptsAndCleansChild(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__pendingNestedDestroys = (window.__pendingNestedDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let visible = true; export let title = "server";</script><template><main>{#await request}{#if visible}<Child :title={title} />{:else}<p class="waiting">Waiting</p>{/if}{:then value}<p class="settled">{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	pendingIf := await.PendingChildren[0].(*IfChainNode)
	pendingChild := pendingIf.Branches[0].Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	ifID, ok := markers.ifID(pendingIf.Span)
	if !ok {
		t.Fatal("pending if marker is missing")
	}
	childScope := "vela-child"
	if suffix := parentPlan.ComponentInstanceSuffixes[pendingChild.Span]; suffix != "" {
		childScope = "vela-parent--" + suffix
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><!--v:vela-parent:" + itoa(ifID) + "--><!--e:" + childScope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + childScope + ":0-->server<!--/v:" + childScope + ":0--></p><!--/e:" + childScope + ":0--><script type=\"application/json\" id=\"vela-state-" + childScope + "\">{\"component\":\"" + childScope + "\",\"props\":{\"Title\":\"server\"},\"state\":{},\"incomplete\":false}</script><!--/v:vela-parent:" + itoa(ifID) + "--><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Visible\":true,\"Title\":\"server\"},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__request = new Promise(resolve => { window.__resolveRequest = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((req,res)=>{const name=req.url==="/"?"index.html":req.url.slice(1);try{res.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,name)))}catch{res.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let browser;try{browser=await chromium.launch({headless:true});const page=await browser.newPage();page.setDefaultTimeout(5000);await page.goto("http://127.0.0.1:"+server.address().port+"/");await page.waitForFunction(()=>window.__hydrated);await page.evaluate(()=>{window.__first=document.querySelector(".child");window.__hydrated.component.set({title:"updated"})});await page.waitForFunction(()=>document.querySelector(".child")?.textContent==="updated");await page.evaluate(()=>window.__hydrated.component.set({visible:false}));await page.waitForFunction(()=>document.querySelector(".waiting"));await page.evaluate(()=>{window.__payloadRemoved=!document.querySelector("[id^=vela-state-vela-parent--]");window.__hydrated.component.set({visible:true})});await page.waitForFunction(()=>document.querySelector(".child")?.textContent==="updated");await page.evaluate(()=>{window.__second=document.querySelector(".child");window.__resolveRequest("done")});await page.waitForFunction(()=>document.querySelector(".settled")?.textContent==="done");const result=await page.evaluate(()=>({initial:window.__initialMutations,firstDetached:!window.__first.isConnected,secondDetached:!window.__second.isConnected,payloadRemoved:window.__payloadRemoved,destroys:window.__pendingNestedDestroys,settled:document.querySelector(".settled")?.textContent}));if(result.initial!==0||!result.firstDetached||!result.secondDetached||!result.payloadRemoved||result.destroys!==2||result.settled!=="done")throw new Error(JSON.stringify(result));process.stdout.write("OK")}catch(error){console.error(error&&error.stack||error);process.exitCode=1}finally{await browser?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pending await-if adoption browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("pending await-if adoption browser output=%q", got)
	}
}

func TestBrowserSSRHydrationPendingAwaitNestedKeyAdoptsAndCleansChild(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__pendingKeyDestroys = (window.__pendingKeyDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let identity = "one"; export let title = "server";</script><template><main>{#await request}{#key identity}<Child :title={title} />{/key}{:then value}<p class="settled">{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	pendingKey := await.PendingChildren[0].(*IfChainNode)
	pendingChild := pendingKey.Branches[0].Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	keyID, ok := markers.ifID(pendingKey.Span)
	if !ok {
		t.Fatal("pending key marker is missing")
	}
	if suffix := parentPlan.ComponentInstanceSuffixes[pendingChild.Span]; suffix != "" {
		t.Fatalf("unexpected static suffix for runtime-keyed child: %q", suffix)
	}
	identity, ok := parentPlan.ComponentInstanceIdentities[pendingChild.Span]
	if !ok || !identity.RuntimeScoped {
		t.Fatalf("pending keyed child identity=%#v, want runtime-scoped", identity)
	}
	childScope := "vela-parent--key-" + itoa(pendingKey.Span.Start) + "-one--" + ssrRuntimeComponentSuffix(identity)
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><!--v:vela-parent:" + itoa(keyID) + "--><!--e:" + childScope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + childScope + ":0-->server<!--/v:" + childScope + ":0--></p><!--/e:" + childScope + ":0--><script type=\"application/json\" id=\"vela-state-" + childScope + "\">{\"component\":\"" + childScope + "\",\"props\":{\"Title\":\"server\"},\"state\":{},\"incomplete\":false}</script><!--/v:vela-parent:" + itoa(keyID) + "--><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Identity\":\"one\",\"Title\":\"server\"},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__request = new Promise(resolve => { window.__resolveRequest = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((req,res)=>{const name=req.url==="/"?"index.html":req.url.slice(1);try{res.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,name)))}catch{res.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let browser;try{browser=await chromium.launch({headless:true});const page=await browser.newPage();page.setDefaultTimeout(5000);await page.goto("http://127.0.0.1:"+server.address().port+"/");await page.waitForFunction(()=>window.__hydrated);await page.evaluate(()=>{window.__first=document.querySelector(".child");window.__hydrated.component.set({title:"updated"})});await page.waitForFunction(()=>document.querySelector(".child")?.textContent==="updated");await page.evaluate(()=>window.__hydrated.component.set({identity:"two"}));await page.waitForFunction(()=>document.querySelector(".child")?.textContent==="updated");await page.evaluate(()=>{window.__second=document.querySelector(".child");window.__payloadRemoved=!document.querySelector("[id^=vela-state-vela-parent--key-]");window.__resolveRequest("done")});await page.waitForFunction(()=>document.querySelector(".settled")?.textContent==="done");const result=await page.evaluate(()=>({initial:window.__initialMutations,firstDetached:!window.__first.isConnected,secondDetached:!window.__second.isConnected,payloadRemoved:window.__payloadRemoved,destroys:window.__pendingKeyDestroys,settled:document.querySelector(".settled")?.textContent}));if(result.initial!==0||!result.firstDetached||!result.secondDetached||!result.payloadRemoved||result.destroys!==2||result.settled!=="done")throw new Error(JSON.stringify(result));process.stdout.write("OK")}catch(error){console.error(error&&error.stack||error);process.exitCode=1}finally{await browser?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pending await-key adoption browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("pending await-key adoption browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAwaitNestedIfOwnsChildTeardown(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__nestedAwaitDestroys = (window.__nestedAwaitDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let visible = true;</script><template><main>{#await request}<p class="loading">Loading</p>{:then value}{#if visible}<Child :title={value} />{:else}<p class="hidden">Hidden</p>{/if}{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(await.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><p vela-parent=\"\" class=\"loading\">Loading</p><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Visible\":true},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js'; window.__request = new Promise(resolve => { window.__resolve = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require('fs'),path=require('path'),http=require('http'),{chromium}=require('playwright');const root=process.argv[1];const server=http.createServer((req,res)=>{const name=req.url==='/'?'index.html':req.url.slice(1);try{res.writeHead(200,{'content-type':name.endsWith('.js')?'text/javascript':'text/html'}).end(fs.readFileSync(path.join(root,name)))}catch{res.writeHead(404).end()}});server.listen(0,'127.0.0.1',async()=>{let browser;try{browser=await chromium.launch({headless:true});const page=await browser.newPage();page.setDefaultTimeout(5000);await page.goto('http://127.0.0.1:'+server.address().port+'/');await page.waitForFunction(()=>window.__hydrated);await page.evaluate(()=>window.__resolve('settled'));await page.waitForFunction(()=>document.querySelector('.child')?.textContent==='settled');await page.evaluate(()=>{window.__first=document.querySelector('.child');window.__hydrated.component.set({visible:false})});await page.waitForFunction(()=>document.querySelector('.hidden'));await page.evaluate(()=>window.__hydrated.component.set({visible:true}));await page.waitForFunction(()=>document.querySelector('.child')?.textContent==='settled');await page.evaluate(()=>{window.__second=document.querySelector('.child');window.__hydrated.component.set({request:new Promise(()=>{})})});await page.waitForFunction(()=>document.querySelector('.loading'));const result=await page.evaluate(()=>({initial:window.__initialMutations,firstDetached:!window.__first.isConnected,secondDetached:!window.__second.isConnected,destroys:window.__nestedAwaitDestroys,child:!!document.querySelector('.child'),loading:!!document.querySelector('.loading')}));if(result.initial!==0||!result.firstDetached||!result.secondDetached||result.destroys!==2||result.child||!result.loading)throw new Error(JSON.stringify(result));process.stdout.write('OK')}catch(error){console.error(error&&error.stack||error);process.exitCode=1}finally{await browser?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested await-if browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested await-if browser output=%q", got)
	}
}

func TestBrowserSSRHydrationEachNestedIfAdoptsAndCleansChildRows(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__eachIfDestroys = (window.__eachIfDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row}{#if row.visible}<Child :title={row.title} />{:else}<p class="hidden">Hidden</p>{/if}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	chain := each.Children[0].(*IfChainNode)
	child := chain.Branches[0].Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("each marker is missing")
	}
	ifID, ok := markers.ifID(chain.Span)
	if !ok {
		t.Fatal("nested if marker is missing")
	}
	identity, ok := parentPlan.ComponentInstanceIdentities[child.Span]
	if !ok || !identity.RuntimeScoped {
		t.Fatalf("nested each-if child identity=%#v, want runtime-scoped", identity)
	}
	rowScope := func(index int) string {
		return "vela-parent--each-" + itoa(each.Span.Start) + "-" + itoa(index) + "--" + ssrRuntimeComponentSuffix(identity)
	}
	childHTML := func(index int, title string) string {
		scope := rowScope(index)
		return "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + scope + ":0-->" + title + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Title\":\"" + title + "\"},\"state\":{},\"incomplete\":false}</script>"
	}
	ifRange := func(html string) string {
		return "<!--v:vela-parent:" + itoa(ifID) + "-->" + html + "<!--/v:vela-parent:" + itoa(ifID) + "-->"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(eachID) + "-->" + ifRange(childHTML(0, "one")) + ifRange("<p vela-parent=\"\" class=\"hidden\">Hidden</p>") + "<!--/v:vela-parent:" + itoa(eachID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"title\":\"one\",\"visible\":true},{\"title\":\"two\",\"visible\":false}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__first=document.querySelector(".child");window.__hydrated.component.set({rows:[{title:"one",visible:false},{title:"two",visible:true}]})});await p.waitForFunction(()=>document.querySelectorAll(".hidden").length===1&&document.querySelector(".child")?.textContent==="two");await p.evaluate(()=>{window.__payloadRemoved=!Array.from(document.querySelectorAll("script")).some(x=>x.id.includes("--each-")&&x.id.includes("-0--"));window.__second=document.querySelector(".child");window.__hydrated.component.destroy()});const x=await p.evaluate(()=>({initial:window.__initialMutations,firstDetached:!window.__first.isConnected,payloadRemoved:window.__payloadRemoved,secondDetached:!window.__second.isConnected,destroys:window.__eachIfDestroys,children:document.querySelectorAll(".child").length}));if(x.initial!==0||!x.firstDetached||!x.payloadRemoved||!x.secondDetached||x.destroys!==2||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested each-if browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested each-if browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachNestedIfRetainsSwitchesAndCleansChildRows(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let id = ""; export let title = ""; onDestroy(() => { window.__keyedEachIfDestroys = (window.__keyedEachIfDestroys || 0) + 1; });</script><template><p class="child" :data-id={id}>{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}{#if row.visible}<Child :id={row.id} :title={row.title} />{:else}<p class="hidden">Hidden</p>{/if}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	chain := each.Children[0].(*IfChainNode)
	child := chain.Branches[0].Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("each marker is missing")
	}
	ifID, ok := markers.ifID(chain.Span)
	if !ok {
		t.Fatal("nested if marker is missing")
	}
	identity := parentPlan.ComponentInstanceIdentities[child.Span]
	if !identity.RuntimeScoped {
		t.Fatal("keyed nested child must be runtime-scoped")
	}
	rowScope := func(key string) string { return "vela-parent--each-" + itoa(each.Span.Start) + "-" + key }
	childHTML := func(key, title string) string {
		scope := rowScope(key) + "--" + ssrRuntimeComponentSuffix(identity)
		return "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\" data-id=\"" + key + "\"><!--v:" + scope + ":0-->" + title + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Id\":\"" + key + "\",\"Title\":\"" + title + "\"},\"state\":{},\"incomplete\":false}</script>"
	}
	ifRange := func(html string) string {
		return "<!--v:vela-parent:" + itoa(ifID) + "-->" + html + "<!--/v:vela-parent:" + itoa(ifID) + "-->"
	}
	rowHTML := func(key, html string) string {
		return "<!--r:" + rowScope(key) + ":" + itoa(eachID) + "-->" + ifRange(html) + "<!--/r:" + rowScope(key) + ":" + itoa(eachID) + "-->"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML("a", childHTML("a", "Alpha")) + rowHTML("b", "<p vela-parent=\"\" class=\"hidden\">Hidden</p>") + "<!--/v:vela-parent:" + itoa(eachID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"id\":\"a\",\"title\":\"Alpha\",\"visible\":true},{\"id\":\"b\",\"title\":\"Beta\",\"visible\":false}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__a=document.querySelector("[data-id=a]");window.__hydrated.component.set({rows:[{id:"b",title:"Beta",visible:true},{id:"a",title:"Alpha*",visible:true},{id:"c",title:"Gamma",visible:false}]})});await p.waitForFunction(()=>Array.from(document.querySelectorAll(".child")).map(x=>x.dataset.id+":"+x.textContent).join("|")==="b:Beta|a:Alpha*"&&document.querySelectorAll(".hidden").length===1);await p.evaluate(()=>{window.__retained=window.__a===document.querySelector("[data-id=a]");window.__hydrated.component.set({rows:[{id:"b",title:"Beta",visible:false},{id:"a",title:"Alpha*",visible:true}]})});await p.waitForFunction(()=>document.querySelectorAll(".hidden").length===1&&document.querySelector("[data-id=a]")?.textContent==="Alpha*");await p.evaluate(()=>window.__hydrated.component.set({rows:[{id:"b",title:"Beta",visible:false}]}));await p.waitForFunction(()=>!document.querySelector("[data-id=a]"));await p.evaluate(()=>{window.__payloadRemoved=!Array.from(document.querySelectorAll("script")).some(x=>x.id.includes("-a--"));window.__hydrated.component.destroy()});const x=await p.evaluate(()=>({initial:window.__initialMutations,retained:window.__retained,payloadRemoved:window.__payloadRemoved,destroys:window.__keyedEachIfDestroys,children:document.querySelectorAll(".child").length}));if(x.initial!==0||!x.retained||!x.payloadRemoved||x.destroys!==2||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed nested each-if browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed nested each-if browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachNestedKeyRetainsReplacesAndCleansChildRows(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let id = ""; export let title = ""; onDestroy(() => { window.__keyedEachKeyDestroys = (window.__keyedEachKeyDestroys || 0) + 1; });</script><template><p class="child" :data-id={id}>{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}{#key row.version}<Child :id={row.id} :title={row.title} />{/key}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	keyBlock := each.Children[0].(*IfChainNode)
	child := keyBlock.Branches[0].Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("each marker is missing")
	}
	keyID, ok := markers.ifID(keyBlock.Span)
	if !ok {
		t.Fatal("nested key marker is missing")
	}
	identity := parentPlan.ComponentInstanceIdentities[child.Span]
	if !identity.RuntimeScoped {
		t.Fatal("keyed nested key child must be runtime-scoped")
	}
	rowScope := func(key string) string { return "vela-parent--each-" + itoa(each.Span.Start) + "-" + key }
	childScope := func(key, version string) string {
		return rowScope(key) + "--key-" + itoa(keyBlock.Span.Start) + "-" + version + "--" + ssrRuntimeComponentSuffix(identity)
	}
	rowHTML := func(key, version, title string) string {
		scope := childScope(key, version)
		childHTML := "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\" data-id=\"" + key + "\"><!--v:" + scope + ":0-->" + title + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Id\":\"" + key + "\",\"Title\":\"" + title + "\"},\"state\":{},\"incomplete\":false}</script>"
		return "<!--r:" + rowScope(key) + ":" + itoa(eachID) + "--><!--v:vela-parent:" + itoa(keyID) + "-->" + childHTML + "<!--/v:vela-parent:" + itoa(keyID) + "--><!--/r:" + rowScope(key) + ":" + itoa(eachID) + "-->"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML("a", "one", "Alpha") + rowHTML("b", "one", "Beta") + "<!--/v:vela-parent:" + itoa(eachID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"id\":\"a\",\"version\":\"one\",\"title\":\"Alpha\"},{\"id\":\"b\",\"version\":\"one\",\"title\":\"Beta\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__a=document.querySelector("[data-id=a]");window.__b=document.querySelector("[data-id=b]");window.__hydrated.component.set({rows:[{id:"b",version:"one",title:"Beta*"},{id:"a",version:"one",title:"Alpha*"}]})});await p.waitForFunction(()=>Array.from(document.querySelectorAll(".child")).map(x=>x.dataset.id+":"+x.textContent).join("|")==="b:Beta*|a:Alpha*");await p.evaluate(()=>{window.__retained=window.__a===document.querySelector("[data-id=a]")&&window.__b===document.querySelector("[data-id=b]");window.__hydrated.component.set({rows:[{id:"b",version:"one",title:"Beta*"},{id:"a",version:"two",title:"Alpha v2"}]})});await p.waitForFunction(()=>document.querySelector("[data-id=a]")?.textContent==="Alpha v2");await p.evaluate(()=>{window.__replaced=window.__a!==document.querySelector("[data-id=a]");window.__hydrated.component.set({rows:[{id:"a",version:"two",title:"Alpha v2"}]})});await p.waitForFunction(()=>document.querySelectorAll(".child").length===1);await p.evaluate(()=>{window.__payloadBRemoved=!Array.from(document.querySelectorAll("script")).some(x=>x.id.includes("--each-")&&x.id.includes("-b--"));window.__hydrated.component.destroy()});const x=await p.evaluate(()=>({initial:window.__initialMutations,retained:window.__retained,replaced:window.__replaced,payloadBRemoved:window.__payloadBRemoved,destroys:window.__keyedEachKeyDestroys,children:document.querySelectorAll(".child").length}));if(x.initial!==0||!x.retained||!x.replaced||!x.payloadBRemoved||x.destroys!==3||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed nested each-key browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed nested each-key browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyNestedAwaitAdoptsRekeysAndCleansChild(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__keyAwaitDestroys = (window.__keyAwaitDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let identity = "one"; export let request;</script><template><main>{#key identity}{#await request}<Child :title={"pending"} />{:then value}<Child :title={value} />{/await}{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	keyBlock := main.Children[0].(*IfChainNode)
	await := keyBlock.Branches[0].Children[0].(*AwaitBlockNode)
	pendingChild := await.PendingChildren[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	keyID, ok := markers.ifID(keyBlock.Span)
	if !ok {
		t.Fatal("key marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("nested await marker is missing")
	}
	identity, ok := parentPlan.ComponentInstanceIdentities[pendingChild.Span]
	if !ok || !identity.RuntimeScoped {
		t.Fatalf("keyed await child identity=%#v, want runtime-scoped", identity)
	}
	scope := "vela-parent--key-" + itoa(keyBlock.Span.Start) + "-one--" + ssrRuntimeComponentSuffix(identity)
	childHTML := "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + scope + ":0-->pending<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Title\":\"pending\"},\"state\":{},\"incomplete\":false}</script>"
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(keyID) + "--><!--v:vela-parent:" + itoa(awaitID) + "-->" + childHTML + "<!--/v:vela-parent:" + itoa(awaitID) + "--><!--/v:vela-parent:" + itoa(keyID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Identity\":\"one\"},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__request = new Promise(resolve => { window.__resolveRequest = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__pending=document.querySelector(".child");window.__resolveRequest("settled")});await p.waitForFunction(()=>document.querySelector(".child")?.textContent==="settled");await p.evaluate(()=>{window.__settled=document.querySelector(".child");window.__payloadRemoved=!document.querySelector("[id^=vela-state-vela-parent--key-]");window.__hydrated.component.set({identity:"two"})});await p.waitForFunction(()=>document.querySelector(".child")?.textContent==="settled"&&document.querySelector(".child")!==window.__settled);await p.evaluate(()=>{window.__rekeyed=document.querySelector(".child");window.__hydrated.component.destroy()});const x=await p.evaluate(()=>({initial:window.__initialMutations,pendingDetached:!window.__pending.isConnected,settledDetached:!window.__settled.isConnected,rekeyedDetached:!window.__rekeyed.isConnected,payloadRemoved:window.__payloadRemoved,destroys:window.__keyAwaitDestroys,children:document.querySelectorAll(".child").length}));if(x.initial!==0||!x.pendingDetached||!x.settledDetached||!x.rekeyedDetached||!x.payloadRemoved||x.destroys!==4||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested key-await browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested key-await browser output=%q", got)
	}
}

func TestBrowserSSRHydrationIfNestedAwaitAdoptsSettlesReentersAndCleansChild(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__ifAwaitDestroys = (window.__ifAwaitDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let shown = true; export let request;</script><template><main>{#if shown}{#await request}<Child :title={"pending"} />{:then value}<Child :title={value} />{/await}{:else}<p class="hidden">Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	chain := main.Children[0].(*IfChainNode)
	await := chain.Branches[0].Children[0].(*AwaitBlockNode)
	pendingChild := await.PendingChildren[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	ifID, ok := markers.ifID(chain.Span)
	if !ok {
		t.Fatal("if marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	identity := parentPlan.ComponentInstanceIdentities[pendingChild.Span]
	suffix := parentPlan.ComponentInstanceSuffixes[pendingChild.Span]
	if identity.RuntimeScoped {
		suffix = ssrRuntimeComponentSuffix(identity)
	}
	if suffix == "" {
		t.Fatal("nested await child scope suffix is missing")
	}
	scope := "vela-parent--" + suffix
	childHTML := "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + scope + ":0-->pending<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Title\":\"pending\"},\"state\":{},\"incomplete\":false}</script>"
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(ifID) + "--><!--v:vela-parent:" + itoa(awaitID) + "-->" + childHTML + "<!--/v:vela-parent:" + itoa(awaitID) + "--><!--/v:vela-parent:" + itoa(ifID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Shown\":true},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__request = new Promise(resolve => { window.__resolveRequest = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__pending=document.querySelector(".child");window.__resolveRequest("settled")});await p.waitForFunction(()=>document.querySelector(".child")?.textContent==="settled");await p.evaluate(()=>{window.__settled=document.querySelector(".child");window.__hydrated.component.set({shown:false})});await p.waitForFunction(()=>document.querySelector(".hidden"));await p.evaluate(()=>window.__hydrated.component.set({shown:true}));await p.waitForFunction(()=>document.querySelector(".child")?.textContent==="settled");await p.evaluate(()=>{window.__reentered=document.querySelector(".child");window.__hydrated.component.destroy()});const x=await p.evaluate(()=>({initial:window.__initialMutations,pendingDetached:!window.__pending.isConnected,settledDetached:!window.__settled.isConnected,reenteredDetached:!window.__reentered.isConnected,destroys:window.__ifAwaitDestroys,children:document.querySelectorAll(".child").length}));if(x.initial!==0||!x.pendingDetached||!x.settledDetached||!x.reenteredDetached||x.destroys!==4||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested if-await browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested if-await browser output=%q", got)
	}
}

func TestBrowserSSRHydrationIfNestedEachAdoptsAndReentersChildRows(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__ifEachDestroys = (window.__ifEachDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let shown = true; export let rows = [];</script><template><main>{#if shown}{#each rows as row}<Child :title={row.title} />{/each}{:else}<p class="hidden">Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	chain := main.Children[0].(*IfChainNode)
	each := chain.Branches[0].Children[0].(*EachBlockNode)
	child := each.Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	ifID, ok := markers.ifID(chain.Span)
	if !ok {
		t.Fatal("if marker is missing")
	}
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("each marker is missing")
	}
	identity, ok := parentPlan.ComponentInstanceIdentities[child.Span]
	if !ok || !identity.RuntimeScoped {
		t.Fatalf("nested each child identity=%#v, want runtime-scoped", identity)
	}
	rowScope := func(index int) string {
		return "vela-parent--each-" + itoa(each.Span.Start) + "-" + itoa(index) + "--" + ssrRuntimeComponentSuffix(identity)
	}
	rowHTML := func(index int, title string) string {
		scope := rowScope(index)
		return "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + scope + ":0-->" + title + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Title\":\"" + title + "\"},\"state\":{},\"incomplete\":false}</script>"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(ifID) + "--><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML(0, "one") + rowHTML(1, "two") + "<!--/v:vela-parent:" + itoa(eachID) + "--><!--/v:vela-parent:" + itoa(ifID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Shown\":true,\"Rows\":[{\"title\":\"one\"},{\"title\":\"two\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__first=Array.from(document.querySelectorAll(".child"));window.__hydrated.component.set({rows:[{title:"next"}]})});await p.waitForFunction(()=>document.querySelectorAll(".child").length===1&&document.querySelector(".child")?.textContent==="next");await p.evaluate(()=>{window.__payloadsRemoved=!document.querySelector("[id^=vela-state-vela-parent--each-]");window.__hydrated.component.set({shown:false})});await p.waitForFunction(()=>document.querySelector(".hidden"));await p.evaluate(()=>window.__hydrated.component.set({shown:true}));await p.waitForFunction(()=>document.querySelectorAll(".child").length===1&&document.querySelector(".child")?.textContent==="next");await p.evaluate(()=>{window.__second=document.querySelector(".child");window.__hydrated.component.destroy()});const x=await p.evaluate(()=>({initial:window.__initialMutations,firstDetached:window.__first.every(n=>!n.isConnected),payloadsRemoved:window.__payloadsRemoved,secondDetached:!window.__second.isConnected,destroys:window.__ifEachDestroys,children:document.querySelectorAll(".child").length}));if(x.initial!==0||!x.firstDetached||!x.payloadsRemoved||!x.secondDetached||x.destroys!==4||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested if-each browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested if-each browser output=%q", got)
	}
}

func TestBrowserSSRHydrationIfNestedKeyedEachRetainsAndReentersChildRows(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let id = ""; export let title = ""; onDestroy(() => { window.__ifKeyedEachDestroys = (window.__ifKeyedEachDestroys || 0) + 1; });</script><template><p class="child" :data-id={id}>{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let shown = true; export let rows = [];</script><template><main>{#if shown}{#each rows as row (row.id)}<Child :id={row.id} :title={row.title} />{/each}{:else}<p class="hidden">Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	chain := main.Children[0].(*IfChainNode)
	each := chain.Branches[0].Children[0].(*EachBlockNode)
	child := each.Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	ifID, ok := markers.ifID(chain.Span)
	if !ok {
		t.Fatal("if marker is missing")
	}
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("each marker is missing")
	}
	identity := parentPlan.ComponentInstanceIdentities[child.Span]
	if !identity.RuntimeScoped {
		t.Fatal("keyed child must be runtime-scoped")
	}
	suffix := ssrRuntimeComponentSuffix(identity)
	rowScope := func(key string) string { return "vela-parent--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, title string) string {
		scope := rowScope(key) + "--" + suffix
		return "<!--r:" + rowScope(key) + ":" + itoa(eachID) + "--><!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\" data-id=\"" + key + "\"><!--v:" + scope + ":0-->" + title + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Id\":\"" + key + "\",\"Title\":\"" + title + "\"},\"state\":{},\"incomplete\":false}</script><!--/r:" + rowScope(key) + ":" + itoa(eachID) + "-->"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(ifID) + "--><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML("a", "Alpha") + rowHTML("b", "Beta") + "<!--/v:vela-parent:" + itoa(eachID) + "--><!--/v:vela-parent:" + itoa(ifID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Shown\":true,\"Rows\":[{\"id\":\"a\",\"title\":\"Alpha\"},{\"id\":\"b\",\"title\":\"Beta\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__a=document.querySelector("[data-id=a]");window.__b=document.querySelector("[data-id=b]");window.__hydrated.component.set({rows:[{id:"b",title:"Beta*"},{id:"a",title:"Alpha*"},{id:"c",title:"Gamma"}]})});await p.waitForFunction(()=>Array.from(document.querySelectorAll(".child")).map(x=>x.dataset.id+":"+x.textContent).join("|")==="b:Beta*|a:Alpha*|c:Gamma");await p.evaluate(()=>{window.__retained=window.__a===document.querySelector("[data-id=a]")&&window.__b===document.querySelector("[data-id=b]");window.__hydrated.component.set({rows:[{id:"b",title:"Final"}]})});await p.waitForFunction(()=>Array.from(document.querySelectorAll(".child")).map(x=>x.dataset.id+":"+x.textContent).join("|")==="b:Final");await p.evaluate(()=>{window.__payloadsRemoved=!Array.from(document.querySelectorAll("script")).some(x=>x.id.includes("-a--"));window.__hydrated.component.set({shown:false})});await p.waitForFunction(()=>document.querySelector(".hidden"));await p.evaluate(()=>window.__hydrated.component.set({shown:true}));await p.waitForFunction(()=>Array.from(document.querySelectorAll(".child")).map(x=>x.dataset.id+":"+x.textContent).join("|")==="b:Final");await p.evaluate(()=>{window.__reentered=Array.from(document.querySelectorAll(".child"));window.__hydrated.component.destroy()});const x=await p.evaluate(()=>({initial:window.__initialMutations,retained:window.__retained,payloadsRemoved:window.__payloadsRemoved,detached:window.__reentered.every(n=>!n.isConnected),destroys:window.__ifKeyedEachDestroys,children:document.querySelectorAll(".child").length}));if(x.initial!==0||!x.retained||!x.payloadsRemoved||!x.detached||x.destroys!==4||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested if-keyed each browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested if-keyed each browser output=%q", got)
	}
}

func TestBrowserSSRHydrationPendingAwaitUnkeyedEachAdoptsAndCleansChildRows(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__pendingEachDestroys = (window.__pendingEachDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let rows = [];</script><template><main>{#await request}{#each rows as row}<Child :title={row.title} />{/each}{:then value}<p class="settled">{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	pendingEach := await.PendingChildren[0].(*EachBlockNode)
	pendingChild := pendingEach.Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	eachID, ok := markers.eachID(pendingEach.Span)
	if !ok {
		t.Fatal("pending each marker is missing")
	}
	identity, ok := parentPlan.ComponentInstanceIdentities[pendingChild.Span]
	if !ok || !identity.RuntimeScoped {
		t.Fatalf("pending each child identity=%#v, want runtime-scoped", identity)
	}
	rowScope := func(index int) string {
		return "vela-parent--each-" + itoa(pendingEach.Span.Start) + "-" + itoa(index) + "--" + ssrRuntimeComponentSuffix(identity)
	}
	rowHTML := func(index int, title string) string {
		scope := rowScope(index)
		return "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + scope + ":0-->" + title + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Title\":\"" + title + "\"},\"state\":{},\"incomplete\":false}</script>"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML(0, "one") + rowHTML(1, "two") + "<!--/v:vela-parent:" + itoa(eachID) + "--><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"title\":\"one\"},{\"title\":\"two\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__request = new Promise(resolve => { window.__resolveRequest = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((req,res)=>{const name=req.url==="/"?"index.html":req.url.slice(1);try{res.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,name)))}catch{res.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let browser;try{browser=await chromium.launch({headless:true});const page=await browser.newPage();page.setDefaultTimeout(5000);await page.goto("http://127.0.0.1:"+server.address().port+"/");await page.waitForFunction(()=>window.__hydrated);await page.evaluate(()=>{window.__first=Array.from(document.querySelectorAll(".child"));window.__hydrated.component.set({rows:[{title:"next"}]})});await page.waitForFunction(()=>document.querySelectorAll(".child").length===1&&document.querySelector(".child")?.textContent==="next");await page.evaluate(()=>{window.__second=document.querySelector(".child");window.__payloadsRemoved=!document.querySelector("[id^=vela-state-vela-parent--each-]");window.__resolveRequest("done")});await page.waitForFunction(()=>document.querySelector(".settled")?.textContent==="done");const result=await page.evaluate(()=>({initial:window.__initialMutations,firstDetached:window.__first.every(node=>!node.isConnected),secondDetached:!window.__second.isConnected,payloadsRemoved:window.__payloadsRemoved,destroys:window.__pendingEachDestroys,settled:document.querySelector(".settled")?.textContent}));if(result.initial!==0||!result.firstDetached||!result.secondDetached||!result.payloadsRemoved||result.destroys!==3||result.settled!=="done")throw new Error(JSON.stringify(result));process.stdout.write("OK")}catch(error){console.error(error&&error.stack||error);process.exitCode=1}finally{await browser?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pending await-each adoption browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("pending await-each adoption browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAwaitNestedEachOwnsChildTeardown(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__nestedEachDestroys = (window.__nestedEachDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request;</script><template><main>{#await request}<p class="loading">Loading</p>{:then items}{#each items as item}<Child :title={item} />{/each}{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(main.Children[0].(*AwaitBlockNode).Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><p vela-parent=\"\" class=\"loading\">Loading</p><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js';window.__request=new Promise(resolve=>{window.__resolve=resolve});window.__observer=new MutationObserver(()=>{});window.__observer.observe(document.body,{childList:true,subtree:true});window.__hydrated=hydrateParent(document.body,{request:window.__request});window.__initialMutations=window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const script = `const fs=require('fs'),path=require('path'),http=require('http'),{chromium}=require('playwright');const root=process.argv[1];const server=http.createServer((q,r)=>{const n=q.url==='/'?'index.html':q.url.slice(1);try{r.writeHead(200,{'content-type':n.endsWith('.js')?'text/javascript':'text/html'}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,'127.0.0.1',async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto('http://127.0.0.1:'+server.address().port+'/');await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>window.__resolve(['one','two']));await p.waitForFunction(()=>Array.from(document.querySelectorAll('.child')).map(n=>n.textContent).join('|')==='one|two');await p.evaluate(()=>{window.__rows=Array.from(document.querySelectorAll('.child'));window.__hydrated.component.set({request:new Promise(()=>{})})});await p.waitForFunction(()=>document.querySelector('.loading'));const x=await p.evaluate(()=>({initial:window.__initialMutations,detached:window.__rows.every(n=>!n.isConnected),destroys:window.__nestedEachDestroys,children:document.querySelectorAll('.child').length}));if(x.initial!==0||!x.detached||x.destroys!==2||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write('OK')}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", script, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested await-each browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested await-each browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAwaitNestedKeyOwnsChildTeardown(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__nestedKeyDestroys = (window.__nestedKeyDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request;</script><template><main>{#await request}<p class="loading">Loading</p>{:then value}{#key value}<Child :title={value} />{/key}{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(main.Children[0].(*AwaitBlockNode).Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	html := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><p vela-parent=\"\" class=\"loading\">Loading</p><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js';window.__request=new Promise(resolve=>{window.__resolve=resolve});window.__observer=new MutationObserver(()=>{});window.__observer.observe(document.body,{childList:true,subtree:true});window.__hydrated=hydrateParent(document.body,{request:window.__request});window.__initialMutations=window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const script = `const fs=require('fs'),path=require('path'),http=require('http'),{chromium}=require('playwright');const root=process.argv[1];const server=http.createServer((q,r)=>{const n=q.url==='/'?'index.html':q.url.slice(1);try{r.writeHead(200,{'content-type':n.endsWith('.js')?'text/javascript':'text/html'}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,'127.0.0.1',async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto('http://127.0.0.1:'+server.address().port+'/');await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>window.__resolve('keyed'));await p.waitForFunction(()=>document.querySelector('.child')?.textContent==='keyed');await p.evaluate(()=>{window.__child=document.querySelector('.child');window.__hydrated.component.set({request:new Promise(()=>{})})});await p.waitForFunction(()=>document.querySelector('.loading'));const x=await p.evaluate(()=>({initial:window.__initialMutations,detached:!window.__child.isConnected,destroys:window.__nestedKeyDestroys,children:document.querySelectorAll('.child').length}));if(x.initial!==0||!x.detached||x.destroys!==1||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write('OK')}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", script, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested await-key browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested await-key browser output=%q", got)
	}
}

func TestBrowserSSRHydrationPendingAwaitKeyedEachAdoptsRetainsAndCleansRows(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let id = ""; export let title = ""; onDestroy(() => { window.__pendingKeyedEachDestroys = (window.__pendingKeyedEachDestroys || 0) + 1; });</script><template><p class="child" :data-id={id}>{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let rows = [];</script><template><main>{#await request}{#each rows as row (row.id)}<Child :id={row.id} :title={row.title} />{/each}{:then value}<p class="settled">{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	each := await.PendingChildren[0].(*EachBlockNode)
	child := each.Children[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker missing")
	}
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("each marker missing")
	}
	identity := parentPlan.ComponentInstanceIdentities[child.Span]
	if !identity.RuntimeScoped {
		t.Fatal("keyed child must be runtime-scoped")
	}
	suffix := ssrRuntimeComponentSuffix(identity)
	childScope := func(key string) string {
		return "vela-parent--each-" + itoa(each.Span.Start) + "-" + key + "--" + suffix
	}
	rowScope := func(key string) string { return "vela-parent--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, title string) string {
		scope := childScope(key)
		return "<!--r:" + rowScope(key) + ":" + itoa(eachID) + "--><!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\" data-id=\"" + key + "\"><!--v:" + scope + ":0-->" + title + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Id\":\"" + key + "\",\"Title\":\"" + title + "\"},\"state\":{},\"incomplete\":false}</script><!--/r:" + rowScope(key) + ":" + itoa(eachID) + "-->"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML("a", "Alpha") + rowHTML("b", "Beta") + "<!--/v:vela-parent:" + itoa(eachID) + "--><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"id\":\"a\",\"title\":\"Alpha\"},{\"id\":\"b\",\"title\":\"Beta\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__request = new Promise(resolve => { window.__resolveRequest = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__a=document.querySelector("[data-id=a]");window.__b=document.querySelector("[data-id=b]");window.__hydrated.component.set({rows:[{id:"b",title:"Beta*"},{id:"a",title:"Alpha*"},{id:"c",title:"Gamma"}]})});await p.waitForFunction(()=>Array.from(document.querySelectorAll(".child")).map(x=>x.dataset.id+":"+x.textContent).join("|")==="b:Beta*|a:Alpha*|c:Gamma");await p.evaluate(()=>{window.__retained=window.__a===document.querySelector("[data-id=a]")&&window.__b===document.querySelector("[data-id=b]");window.__hydrated.component.set({rows:[{id:"b",title:"Final"}]})});await p.waitForFunction(()=>document.querySelectorAll(".child").length===1&&document.querySelector(".child")?.textContent==="Final");await p.evaluate(()=>{window.__payloadA=!Array.from(document.querySelectorAll("script")).some(x=>x.id.includes("-a--"));window.__resolveRequest("done")});await p.waitForFunction(()=>document.querySelector(".settled")?.textContent==="done");const x=await p.evaluate(()=>({initial:window.__initialMutations,retained:window.__retained,payloadA:window.__payloadA,destroys:window.__pendingKeyedEachDestroys}));if(x.initial!==0||!x.retained||!x.payloadA||x.destroys!==3)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pending await-keyed each browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("pending await-keyed each browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAwaitNestedKeyedEachOwnsChildTeardown(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__nestedKeyedEachDestroys = (window.__nestedKeyedEachDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request;</script><template><main>{#await request}<p class="loading">Loading</p>{:then items}{#each items as item (item.id)}<Child :title={item.title} />{/each}{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(main.Children[0].(*AwaitBlockNode).Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	html := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(awaitID) + "--><p vela-parent=\"\" class=\"loading\">Loading</p><!--/v:vela-parent:" + itoa(awaitID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js';window.__request=new Promise(resolve=>{window.__resolve=resolve});window.__observer=new MutationObserver(()=>{});window.__observer.observe(document.body,{childList:true,subtree:true});window.__hydrated=hydrateParent(document.body,{request:window.__request});window.__initialMutations=window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const script = `const fs=require('fs'),path=require('path'),http=require('http'),{chromium}=require('playwright');const root=process.argv[1];const server=http.createServer((q,r)=>{const n=q.url==='/'?'index.html':q.url.slice(1);try{r.writeHead(200,{'content-type':n.endsWith('.js')?'text/javascript':'text/html'}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,'127.0.0.1',async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto('http://127.0.0.1:'+server.address().port+'/');await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>window.__resolve([{id:'a',title:'one'},{id:'b',title:'two'}]));await p.waitForFunction(()=>Array.from(document.querySelectorAll('.child')).map(n=>n.textContent).join('|')==='one|two');await p.evaluate(()=>{window.__rows=Array.from(document.querySelectorAll('.child'));window.__hydrated.component.set({request:new Promise(()=>{})})});await p.waitForFunction(()=>document.querySelector('.loading'));const x=await p.evaluate(()=>({initial:window.__initialMutations,detached:window.__rows.every(n=>!n.isConnected),destroys:window.__nestedKeyedEachDestroys,children:document.querySelectorAll('.child').length}));if(x.initial!==0||!x.detached||x.destroys!==2||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write('OK')}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", script, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested await-keyed-each browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested await-keyed-each browser output=%q", got)
	}
}

func TestSSRHydrationClientPassesNestedElementToLocalChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela";</script><template><main><section><Child /></section></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `Child($$n1).component`) {
		t.Fatalf("nested child hydration must receive adopted parent element:\n%s", source)
	}
}

func TestSSRProjectHydratesNestedLocalComponentGraph(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	grandchild := filepath.Join(srcRoot, "Grandchild.vela")
	if err := os.WriteFile(grandchild, []byte(`<script>export let title = "";</script><template><span class="grandchild">{title}</span></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<script>import { Grandchild } from "./Grandchild.vela"; export let title = "";</script><template><section><Grandchild :title={title} /></section></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child :title={title} /></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{grandchild, child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	childClient, err := os.ReadFile(layout.hydrationClientPath(child, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(childClient), `Grandchild.hydrate.js`) {
		t.Fatalf("nested child hydration client misses grandchild entry:\n%s", childClient)
	}
}

func TestSSRHydrationClientAttachesComponentEventsAfterChildAdoption(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; let count = $state(0);</script><template><main><Child @on:close={count++} /><p>{count}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	child := strings.Index(source, `Child($$root).component`)
	listener := strings.Index(source, `.addEventListener(`)
	if child < 0 || listener < 0 || listener < child {
		t.Fatalf("component event listener must follow child adoption:\n%s", source)
	}
	if !strings.Contains(source, `instanceof CustomEvent`) {
		t.Fatalf("component event listener must retain CustomEvent guard:\n%s", source)
	}
}

func TestBrowserSSRHydrationForwardsLocalChildComponentEvent(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<template><button class="child" @click={() => $$dispatch("close")}>close</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; let count = $state(0);</script><template><main><Child @on:close={count++} /><p class="count">{count}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--e:vela-child:0--><button vela-child="" class="child">close</button><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{},"state":{},"incomplete":false}</script><!--e:vela-parent:1--><p vela-parent="" class="count"><!--v:vela-parent:0-->0<!--/v:vela-parent:0--></p><!--/e:vela-parent:1--></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{},"state":{"count":0},"incomplete":false}</script>
<script type="module">
  import hydrateParent from "./Parent.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydrateParent(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require("fs");
const path = require("path");
const http = require("http");
const { chromium } = require("playwright");
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === "/" ? "index.html" : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {"content-type": name.endsWith(".js") ? "text/javascript" : "text/html"}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => { window.__child = document.querySelector(".child"); });
    await page.locator(".child").click();
    await page.waitForFunction(() => document.querySelector(".count")?.textContent === "1");
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, same: document.querySelector(".child") === window.__child, count: document.querySelector(".count")?.textContent }));
    if (result.initial !== 0 || !result.same || result.count !== "1") throw new Error(JSON.stringify(result));
    process.stdout.write("OK");
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("component event hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("component event hydration browser output=%q", got)
	}
}

func TestSSRHydrationClientPassesBindableCallbackBeforeChildAdoption(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let value = "server";</script><template><main><Child bind:value={value} /><p>{value}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`Child($$root, { $$onChange_value: ($$v) => $$set_value($$v) }).component`,
		`.set({ value: value, $$onChange_value:`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("bindable child hydration lowering misses %q:\n%s", want, source)
		}
	}
}

func TestBrowserSSRHydrationBindsLocalChildValueWithoutInitialRewrite(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let value = $bindable("");</script><template><input class="child" bind:value={value} /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let value = "server";</script><template><main><Child bind:value={value} /><p class="value">{value}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--e:vela-child:0--><input vela-child="" class="child" value="server"><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{"Value":"server"},"state":{},"incomplete":false}</script><!--e:vela-parent:1--><p vela-parent="" class="value"><!--v:vela-parent:0-->server<!--/v:vela-parent:0--></p><!--/e:vela-parent:1--></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Value":"server"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydrateParent from "./Parent.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydrateParent(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require("fs");
const path = require("path");
const http = require("http");
const { chromium } = require("playwright");
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === "/" ? "index.html" : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {"content-type": name.endsWith(".js") ? "text/javascript" : "text/html"}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => { window.__input = document.querySelector(".child"); });
    await page.locator(".child").fill("client");
    await page.waitForFunction(() => document.querySelector(".value")?.textContent === "client");
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, same: document.querySelector(".child") === window.__input, value: document.querySelector(".child")?.value, text: document.querySelector(".value")?.textContent }));
    if (result.initial !== 0 || !result.same || result.value !== "client" || result.text !== "client") throw new Error(JSON.stringify(result));
    process.stdout.write("OK");
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bindable child hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("bindable child hydration browser output=%q", got)
	}
}

func TestSSRProjectBuildsBindableLocalChildHydration(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<script>export let value = $bindable("");</script><template><input bind:value={value} /></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela"; export let value = "server";</script><template><main><Child bind:value={value} /><p>{value}</p></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	client, err := os.ReadFile(layout.hydrationClientPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(client), `$$onChange_value`) {
		t.Fatalf("parent hydration client misses bindable callback:\n%s", client)
	}
}

func TestSSRHydrationClientAdoptsStaticLocalComponentSlots(t *testing.T) {
	child, err := parseSFC(`<template><section><slot><p>fallback</p></slot></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(child.TemplateAST.Nodes) {
		t.Fatal("static child slot should be hydration-capable")
	}
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela";</script><template><main><Child><p class="slotted">body</p></Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", parent, analyzeScriptAST(parent.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `Child($$root).component`) || strings.Contains(source, `createComment("comp")`) {
		t.Fatalf("static slot child must adopt existing DOM without CSR anchor:\n%s", source)
	}
}

func TestSSRProjectBuildsStaticLocalComponentSlotsForHydration(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<template><section><slot><p>fallback</p></slot></section></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela";</script><template><main><Child><p class="slotted">body</p></Child></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	if _, err := os.Stat(layout.hydrationClientPath(parent, srcRoot)); err != nil {
		t.Fatalf("parent static-slot hydration client: %v", err)
	}
}

func TestBrowserSSRHydrationAdoptsStructuralAndReactiveLocalComponentSlotContent(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<template><section class="child"><slot><p>fallback</p></slot></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let visible = true; export let title = "server"; export let selected = false; function toggle() { selected = !selected; }</script><template><main><Child>{#if visible}<p class="slot-state">shown</p>{:else}<p class="slot-state">hidden</p>{/if}<button class="slotted" :title={title} class:active={selected} @click={toggle}>{title}</button></Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--e:vela-child:0--><section vela-child="" class="child"><!--v:vela-parent:0--><!--e:vela-parent:1--><p vela-parent="" class="slot-state">shown</p><!--/e:vela-parent:1--><!--/v:vela-parent:0--><!--e:vela-parent:3--><button vela-parent="" class="slotted" title="server"><!--v:vela-parent:1-->server<!--/v:vela-parent:1--></button><!--/e:vela-parent:3--></section><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{},"state":{},"incomplete":false}</script></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Visible":true,"Title":"server","Selected":false},"state":{},"incomplete":false}</script>
<script type="module">
  import hydrateParent from "./Parent.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__slot = document.querySelector(".slotted");
  window.__hydrated = hydrateParent(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require("fs");
const path = require("path");
const http = require("http");
const { chromium } = require("playwright");
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === "/" ? "index.html" : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {"content-type": name.endsWith(".js") ? "text/javascript" : "text/html"}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(String(error.stack || error)));
    page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    try { await page.waitForFunction(() => window.__hydrated); }
    catch (error) { throw new Error(String(error.stack || error) + "\n" + errors.join("\n")); }
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => window.__hydrated.component.set({title: "client"}));
    await page.waitForFunction(() => document.querySelector(".slotted")?.textContent === "client" && document.querySelector(".slotted")?.getAttribute("title") === "client");
    await page.evaluate(() => document.querySelector(".slotted").click());
    await page.waitForFunction(() => document.querySelector(".slotted")?.classList.contains("active"));
    await page.evaluate(() => window.__hydrated.component.set({visible: false}));
    await page.waitForFunction(() => document.querySelector(".slot-state")?.textContent === "hidden");
    const result = await page.evaluate((initial) => ({ initial, same: document.querySelector(".slotted") === window.__slot, text: document.querySelector(".slotted")?.textContent, title: document.querySelector(".slotted")?.getAttribute("title"), active: document.querySelector(".slotted")?.classList.contains("active"), state: document.querySelector(".slot-state")?.textContent }), initial);
    if (result.initial !== 0 || !result.same || result.text !== "client" || result.title !== "client" || !result.active || result.state !== "hidden") throw new Error(JSON.stringify(result));
    process.stdout.write("OK");
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("reactive slot element hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("reactive slot element hydration browser output=%q", got)
	}
}

func TestSSRHydrationClientAdoptsDynamicLocalComponentSlotText(t *testing.T) {
	child, err := parseSFC(`<template><section><slot><p>fallback</p></slot></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child>{title}</Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(child.TemplateAST.Nodes) || !CanBuildSSRHydrationClient(parent.TemplateAST.Nodes) {
		t.Fatal("dynamic text slot should be hydration-capable")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", parent, analyzeScriptAST(parent.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `Child($$root).component`) || !strings.Contains(source, `$$acquireHydrationRange($$walker, 0)`) || !strings.Contains(source, `$$writeHydrationText`) {
		t.Fatalf("dynamic parent slot must acquire and update its own text range:\n%s", source)
	}
}

func TestSSRHydrationClientAdoptsReactiveLocalComponentSlotElement(t *testing.T) {
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let title = "server"; let active = $state(false); function toggle() { active = !active; }</script><template><main><Child><button :title={title} class:active={active} @click={toggle}>{title}</button></Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(parent.TemplateAST.Nodes) {
		t.Fatal("reactive ordinary element slot should be hydration-capable")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", parent, analyzeScriptAST(parent.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `$$acquireHydrationElement($$walker, 1, 'button')`) || !strings.Contains(source, `addEventListener('click'`) || !strings.Contains(source, `$$writeHydrationText`) {
		t.Fatalf("reactive slot element must be acquired and retain its parent updates/listener:\n%s", source)
	}
}

func TestSSRHydrationRejectsAwaitLocalComponentSlotContent(t *testing.T) {
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela";</script><template><main><Child>{#await pending}<p>body</p>{/await}</Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if CanBuildSSRHydrationClient(parent.TemplateAST.Nodes) {
		t.Fatal("await slot content must stay outside the hydration subset")
	}
}

func TestSSRHydrationClientUsesCanonicalElementMarkersAfterStructuralSlot(t *testing.T) {
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let visible = true; export let title = "server"; function save() { title = "client"; }</script><template><main><Child>{#if visible}<p>branch</p>{/if}<button @click={save}>{title}</button></Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(parent.TemplateAST.Nodes) {
		t.Fatal("static structural slot followed by reactive sibling should be hydration-capable")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", parent, analyzeScriptAST(parent.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `$$acquireHydrationElement($$walker, 2, 'button')`) || strings.Contains(source, `$$acquireHydrationElement($$walker, 1, 'button')`) {
		t.Fatalf("reactive sibling must keep the SSR-plan marker after a structural slot range:\n%s", source)
	}
}

func TestBrowserSSRHydrationUnkeyedEachNestedAwaitAdoptsSettlesAndRebuildsOnce(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.__eachAwaitDestroys = (window.__eachAwaitDestroys || 0) + 1; });</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let rows = [];</script><template><main>{#each rows as row}{#await request}<Child :title={row.title} />{:then value}<p class="settled">Done</p>{:catch error}<p class="failed">Failed</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	child := await.PendingChildren[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("outer each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("nested await marker is missing")
	}
	identity := parentPlan.ComponentInstanceIdentities[child.Span]
	if !identity.RuntimeScoped {
		t.Fatal("nested await child must be runtime-scoped")
	}
	rowScope := func(index int) string {
		return "vela-parent--each-" + itoa(each.Span.Start) + "-" + itoa(index) + "--" + ssrRuntimeComponentSuffix(identity)
	}
	rowHTML := func(index int, title string) string {
		scope := rowScope(index)
		return "<!--v:vela-parent:" + itoa(awaitID) + "--><!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\"><!--v:" + scope + ":0-->" + title + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Title\":\"" + title + "\"},\"state\":{},\"incomplete\":false}</script><!--/v:vela-parent:" + itoa(awaitID) + "-->"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(eachID) + "-->" + rowHTML(0, "one") + rowHTML(1, "two") + "<!--/v:vela-parent:" + itoa(eachID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"title\":\"one\"},{\"title\":\"two\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from \"./Parent.hydrate.js\"; window.__request = new Promise(resolve => { window.__resolveOld = resolve; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { request: window.__request }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__first=Array.from(document.querySelectorAll(".child"));window.__nextRequest=new Promise(resolve=>{window.__resolveNext=resolve});window.__hydrated.component.set({rows:[{title:"next"}],request:window.__nextRequest})});await p.waitForFunction(()=>document.querySelectorAll(".child").length===1&&document.querySelector(".child")?.textContent==="next");await p.evaluate(()=>{window.__next=document.querySelector(".child");window.__payloadsRemoved=!document.querySelector("[id^=vela-state-vela-parent--each-]");window.__resolveNext("done")});await p.waitForFunction(()=>document.querySelectorAll(".settled").length===1);await p.evaluate(()=>window.__hydrated.component.destroy());const x=await p.evaluate(()=>({initial:window.__initialMutations,firstDetached:window.__first.every(n=>!n.isConnected),nextDetached:!window.__next.isConnected,payloadsRemoved:window.__payloadsRemoved,destroys:window.__eachAwaitDestroys,children:document.querySelectorAll(".child").length,settled:document.querySelectorAll(".settled").length}));if(x.initial!==0||!x.firstDetached||!x.nextDetached||!x.payloadsRemoved||x.destroys!==3||x.children!==0||x.settled!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested each-await browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested each-await browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachPerRowAwaitAdoptsSettlesReordersAndSuppressesLateCallbacks(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let id = ""; export let title = ""; onDestroy(() => { window.__keyedRowAwaitDestroys = (window.__keyedRowAwaitDestroys || 0) + 1; });</script><template><p class="child" :data-id={id}>{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}{#await row.request}<Child :id={row.id} :title={row.title} />{:then value}<Child :id={row.id} :title={value} />{:catch error}<Child :id={row.id} :title={error} />{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	child := await.PendingChildren[0].(*ComponentNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("per-row await marker is missing")
	}
	identity := parentPlan.ComponentInstanceIdentities[child.Span]
	if !identity.RuntimeScoped {
		t.Fatal("per-row pending child must be runtime-scoped")
	}
	rowScope := func(key string) string { return "vela-parent--each-" + itoa(each.Span.Start) + "-" + key }
	childHTML := func(key, title string) string {
		scope := rowScope(key) + "--" + ssrRuntimeComponentSuffix(identity)
		return `<!--e:` + scope + `:0--><p vela-child="" class="child" data-id="` + key + `"><!--v:` + scope + `:0-->` + title + `<!--/v:` + scope + `:0--></p><!--/e:` + scope + `:0--><script type="application/json" id="vela-state-` + scope + `">{"component":"` + scope + `","props":{"Id":"` + key + `","Title":"` + title + `"},"state":{},"incomplete":false}</script>`
	}
	rowHTML := func(key, title string) string {
		return `<!--r:` + rowScope(key) + `:` + itoa(eachID) + `--><!--v:vela-parent:` + itoa(awaitID) + `-->` + childHTML(key, title) + `<!--/v:vela-parent:` + itoa(awaitID) + `--><!--/r:` + rowScope(key) + `:` + itoa(eachID) + `-->`
	}
	serverHTML := `<!doctype html><body><!--e:vela-parent:0--><main vela-parent=""><!--v:vela-parent:` + itoa(eachID) + `-->` + rowHTML("a", "Alpha") + rowHTML("b", "Beta") + `<!--/v:vela-parent:` + itoa(eachID) + `--></main><!--/e:vela-parent:0--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Rows":[{"id":"a","title":"Alpha"},{"id":"b","title":"Beta"}]},"state":{},"incomplete":false}</script><script type="module">import hydrateParent from "./Parent.hydrate.js"; window.__aPromise = new Promise(resolve => { window.__resolveA = resolve; }); window.__bPromise = new Promise((resolve, reject) => { window.__resolveOldB = resolve; window.__rejectOldB = reject; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { rows: [{id:"a",title:"Alpha",request:window.__aPromise},{id:"b",title:"Beta",request:window.__bPromise}] }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__bNode=document.querySelector("[data-id=b]");window.__resolveA("A done")});await p.waitForFunction(()=>document.querySelector("[data-id=a]")?.textContent==="A done");await p.evaluate(()=>window.__hydrated.component.set({rows:[{id:"b",title:"Beta*",request:window.__bPromise},{id:"a",title:"Alpha",request:window.__aPromise}]}));await p.waitForFunction(()=>Array.from(document.querySelectorAll(".child")).map(x=>x.dataset.id+":"+x.textContent).join("|")==="b:Beta*|a:A done");await p.evaluate(()=>{window.__retained=window.__bNode===document.querySelector("[data-id=b]");window.__newBPromise=new Promise((resolve,reject)=>{window.__resolveNewB=resolve;window.__rejectNewB=reject});window.__hydrated.component.set({rows:[{id:"b",title:"Beta new",request:window.__newBPromise},{id:"a",title:"Alpha",request:window.__aPromise}]})});await p.waitForFunction(()=>document.querySelector("[data-id=b]")?.textContent==="Beta new");await p.evaluate(()=>{window.__oldBDetached=!window.__bNode.isConnected;window.__resolveOldB("late")});await p.waitForTimeout(20);await p.evaluate(()=>window.__rejectNewB("bad"));await p.waitForFunction(()=>document.querySelector("[data-id=b]")?.textContent==="bad");await p.evaluate(()=>window.__hydrated.component.set({rows:[{id:"b",title:"Beta new",request:window.__newBPromise}]}));await p.waitForFunction(()=>!document.querySelector("[data-id=a]"));await p.evaluate(()=>{window.__payloadsRemoved=!document.querySelector("[id^=vela-state-vela-parent--each-]");window.__hydrated.component.destroy()});const x=await p.evaluate(()=>({initial:window.__initialMutations,retained:window.__retained,oldBDetached:window.__oldBDetached,payloadsRemoved:window.__payloadsRemoved,destroys:window.__keyedRowAwaitDestroys,children:document.querySelectorAll(".child").length}));if(x.initial!==0||!x.retained||!x.oldBDetached||!x.payloadsRemoved||x.destroys!==5||x.children!==0)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed per-row each-await browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed per-row each-await browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitSettledTextUpdatesPerRow(t *testing.T) {
	node := requirePlaywright(t)
	parentSFC, err := parseSFC(`<script>export let rows = []; export let prefix = "ready: "; export let active = false; export let color = "red"; export let eventLog = ""; export let draft = ""; export let checked = false; export let show = false; export let labels = [];</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{prefix}{value}<p class="marker" :data-label={prefix} class:active={active} style:color={color}>Done</p><button class="action" @click={() => eventLog = eventLog + row.id + ":" + value}>Record</button>{eventLog}<input class="draft" bind:value={draft} /><input class="checked" type="checkbox" bind:checked={checked} /><p class="binding-output">{draft}:{checked}</p>{#if show}<p class="conditional" :data-draft={draft}>Shown {draft}:{checked}</p>{:else}<p class="conditional">Hidden</p>{/if}{#each labels as label}<p class="nested-label" :data-label={label} class:selected={label === "two"} style:--label={label} @click={() => eventLog = eventLog + label}>{label}</p>{:else}<p class="nested-empty">Empty</p>{/each}<section class="nested"><p class="nested-output" :data-draft={draft}>{draft}:{checked}</p></section>{:catch error}{prefix}{error}<p class="marker" :data-label={prefix} class:active={active} style:color={color}>Failed</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("per-row await marker is missing")
	}
	rowScope := func(key string) string { return "vela-parent--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key string) string {
		return `<!--r:` + rowScope(key) + `:` + itoa(eachID) + `--><!--v:vela-parent:` + itoa(awaitID) + `--><p vela-parent="" class="pending">Loading</p><!--/v:vela-parent:` + itoa(awaitID) + `--><!--/r:` + rowScope(key) + `:` + itoa(eachID) + `-->`
	}
	serverHTML := `<!doctype html><body><!--e:vela-parent:0--><main vela-parent=""><!--v:vela-parent:` + itoa(eachID) + `-->` + rowHTML("a") + rowHTML("b") + `<!--/v:vela-parent:` + itoa(eachID) + `--></main><!--/e:vela-parent:0--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Rows":[{"id":"a"},{"id":"b"}],"Prefix":"ready: "},"state":{},"incomplete":false}</script><script type="module">import hydrateParent from "./Parent.hydrate.js"; window.__aPromise = new Promise(resolve => { window.__resolveA = resolve; }); window.__bPromise = new Promise((resolve, reject) => { window.__resolveB = resolve; window.__rejectB = reject; }); window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body, { rows: [{id:"a",request:window.__aPromise},{id:"b",request:window.__bPromise}] }); window.__initialMutations = window.__observer.takeRecords().length;</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.__hydrated);await p.evaluate(()=>{window.__pendingB=document.querySelectorAll(".pending")[1];window.__resolveA("A")});await p.waitForFunction(()=>document.querySelector("main")?.textContent.includes("ready: A"));await p.evaluate(()=>{window.__oldAction=document.querySelector(".action");window.__oldAction.click()});await p.waitForFunction(()=>document.querySelector("main")?.textContent.includes("a:A"));await p.evaluate(async()=>{window.__oldAction.click();await new Promise(resolve=>setTimeout(resolve,0));window.__staleEventText=document.querySelector("main").textContent});if((await p.evaluate(()=>window.__staleEventText)).includes("a:Aa:A"))throw new Error("stale event listener remained attached");await p.evaluate(()=>{const input=document.querySelector(".draft");input.value="typed";input.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>document.querySelector("main")?.textContent.includes("typed:false")&&document.querySelector(".nested-output")?.dataset.draft==="typed");await p.evaluate(()=>document.querySelector(".checked").click());await p.waitForFunction(()=>document.querySelector("main")?.textContent.includes("typed:true")&&document.querySelector(".nested-output")?.textContent==="typed:true");await p.evaluate(()=>window.__hydrated.component.set({draft:"server",checked:false}));await p.waitForFunction(()=>document.querySelector(".draft")?.value==="server"&&!document.querySelector(".checked")?.checked&&document.querySelector("main")?.textContent.includes("server:false")&&document.querySelector(".nested-output")?.dataset.draft==="server");await p.evaluate(()=>window.__hydrated.component.set({show:true,labels:["one","two"]}));await p.waitForFunction(()=>document.querySelector(".conditional")?.textContent==="Shown server:false"&&document.querySelector(".conditional")?.dataset.draft==="server"&&Array.from(document.querySelectorAll(".nested-label")).map(node=>node.textContent+":"+node.dataset.label+":"+node.classList.contains("selected")+":"+node.style.getPropertyValue("--label")).join("|")==="one:one:false:one|two:two:true:two");await p.evaluate(()=>{window.__oldNestedLabel=document.querySelectorAll(".nested-label")[1];window.__oldNestedLabel.click()});await p.waitForFunction(()=>document.querySelector("main")?.textContent.includes("a:Atwo"));await p.evaluate(()=>window.__hydrated.component.set({draft:"after",checked:true}));await p.waitForFunction(()=>document.querySelector(".conditional")?.textContent==="Shown after:true"&&document.querySelector(".conditional")?.dataset.draft==="after");await p.evaluate(()=>window.__hydrated.component.set({labels:["next"]}));await p.waitForFunction(()=>document.querySelector(".nested-label")?.textContent==="next"&&document.querySelector(".nested-label")?.dataset.label==="next"&&!document.querySelector(".nested-label")?.classList.contains("selected")&&document.querySelector(".nested-label")?.style.getPropertyValue("--label")==="next");await p.evaluate(async()=>{window.__oldNestedLabel.click();await new Promise(resolve=>setTimeout(resolve,0));window.__staleNestedEvent=document.querySelector("main").textContent});if((await p.evaluate(()=>window.__staleNestedEvent)).includes("a:Atwotwo"))throw new Error("stale nested item listener remained attached");await p.evaluate(()=>window.__hydrated.component.set({labels:[]}));await p.waitForFunction(()=>document.querySelector(".nested-empty")?.textContent==="Empty");await p.evaluate(()=>window.__hydrated.component.set({prefix:"next: ",active:true,color:"blue"}));await p.waitForFunction(()=>{const marker=document.querySelector(".marker");return document.querySelector("main")?.textContent.includes("next: A")&&marker?.dataset.label==="next: "&&marker.classList.contains("active")&&marker.style.color==="blue"});await p.evaluate(()=>{window.__pendingBRetained=window.__pendingB===document.querySelector(".pending");window.__rejectB("B failed")});await p.waitForFunction(()=>document.querySelector("main")?.textContent.includes("next: B failed"));await p.evaluate(()=>window.__hydrated.component.destroy());const x=await p.evaluate(()=>({initial:window.__initialMutations,pendingBRetained:window.__pendingBRetained,main:document.querySelector("main")}));if(x.initial!==0||!x.pendingBRetained||x.main)throw new Error(JSON.stringify(x));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed each-await settled text browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed each-await settled text browser output=%q", got)
	}
}

func TestSSRHydrationKeyedEachAwaitOwnsNestedRowControllers(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Loading</p>{:then value}{#each row.fields as field, index (field.id)}<p>{field.title}</p>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"innerRows: new Map()", "innerRows.forEach", "innerRows.clear", "controller", "innerRefresh", "reconcileHydrationKeyedRowRanges", "$$findHydrationNestedRange", "$$writeHydrationText", "$$createHydrationRowController", "$$eachRowsForMutations", "$$activeMutations_rows", "$$dirtyRows"} {
		if !strings.Contains(code, want) {
			t.Fatalf("keyed await row ownership misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitReadsScopedSnapshotForStaticSettledRows(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}<p class="settled">Settled</p>{:catch error}<Child :title={error} />{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"snapshot: -1",
		"$$props.$$velaAwaitAt && $$props.$$velaAwaitAt[$$instanceID + '--each-",
		"_snapshot.state === 'then'",
		"$$state.snapshot === 1",
		"$$state.snapshot = -1",
		"_mount = (",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("keyed await scoped snapshot lowering misses %q:\n%s", want, code)
		}
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsScopedStaticSnapshots(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let title = "";</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Pending</p>{:then value}<p class="settled">Settled</p>{:catch error}<Child :title={error} />{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("keyed await marker is missing")
	}
	rowScope := func(key string) string { return "vela-parent--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key string) string {
		scope := rowScope(key)
		return `<!--r:` + scope + `:` + itoa(eachID) + `--><!--v:vela-parent:` + itoa(awaitID) + `--><p vela-parent="" class="settled">Settled</p><!--/v:vela-parent:` + itoa(awaitID) + `--><!--/r:` + scope + `:` + itoa(eachID) + `-->`
	}
	serverHTML := `<!doctype html><body><!--e:vela-parent:0--><main vela-parent=""><!--v:vela-parent:` + itoa(eachID) + `-->` + rowHTML("a") + rowHTML("b") + `<!--/v:vela-parent:` + itoa(eachID) + `--></main><!--/e:vela-parent:0--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Rows":[{"id":"a"},{"id":"b"}]},"state":{},"incomplete":false,"awaitAt":{"` + rowScope("a") + `:` + itoa(awaitID) + `":{"state":"then","value":"server-a"},"` + rowScope("b") + `:` + itoa(awaitID) + `":{"state":"then","value":"server-b"}}}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.promiseA=new Promise(resolve=>{window.resolveA=resolve});window.promiseB=new Promise(resolve=>{window.resolveB=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydrateParent(document.body,{rows:[{id:"a",request:window.promiseA},{id:"b",request:window.promiseB}]});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(serverHTML),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("scoped settled hydration mutated DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".settled")[0];window.b=document.querySelectorAll(".settled")[1];window.resolveA("same")});await p.waitForFunction(()=>document.querySelectorAll(".settled")[0]===window.a);await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",request:window.promiseB},{id:"a",request:window.promiseA}]}));await p.waitForFunction(()=>document.querySelectorAll(".settled")[0]===window.b&&document.querySelectorAll(".settled")[1]===window.a);await p.evaluate(()=>{window.nextA=new Promise(resolve=>{window.resolveNextA=resolve});window.hydrated.component.set({rows:[{id:"b",request:window.promiseB},{id:"a",request:window.nextA}]})});await p.waitForFunction(()=>document.querySelectorAll(".pending").length===1);await p.evaluate(()=>window.resolveNextA("again"));await p.waitForFunction(()=>document.querySelectorAll(".settled").length===2);const state=await p.evaluate(()=>({aDetached:!window.a.isConnected,bRetained:document.querySelectorAll(".settled")[0]===window.b}));if(!state.aDetached||!state.bRetained)throw new Error(JSON.stringify(state));await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed await scoped static snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed await scoped static snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedTextRows(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let rows = [];</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Pending</p>{:then value}{#each row.fields as field (field.id)}<p class="field" :title={field.title} class:selected={field.selected} style:color={field.color} @click={() => window.__nestedClicks = window.__nestedClicks + 1}>{field.title}</p>{/each}{:catch error}<p class="failed">Failed</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, ok := markers.eachID(outer.Span)
	if !ok {
		t.Fatal("outer keyed each marker is missing")
	}
	innerID, ok := markers.eachID(inner.Span)
	if !ok {
		t.Fatal("inner keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("nested await marker is missing")
	}
	mainPlan := plan.Nodes[0].(*SSRPlanElement)
	outerPlan := mainPlan.Children[0].(*SSRPlanEach)
	awaitPlan := outerPlan.Children[0].(*SSRPlanAwait)
	innerPlan := awaitPlan.ThenChildren[0].(*SSRPlanEach)
	fieldPlan := innerPlan.Children[0].(*SSRPlanElement)
	fieldTextPlan := fieldPlan.Children[0].(*SSRPlanText)
	textID := fieldTextPlan.Parts[0].DynamicID
	outerScope := func(key string) string { return "vela-page--each-" + itoa(outer.Span.Start) + "-" + key }
	innerScope := func(outerKey, fieldKey string) string {
		return outerScope(outerKey) + "--each-" + itoa(inner.Span.Start) + "-" + fieldKey
	}
	fieldHTML := func(outerKey, fieldKey, title string, selected bool, color string) string {
		scope := innerScope(outerKey, fieldKey)
		class := "field"
		if selected {
			class += " selected"
		}
		return `<!--r:` + scope + `:` + itoa(innerID) + `--><!--e:` + scope + `:` + itoa(fieldPlan.HydrationID) + `--><p vela-page="" class="` + class + `" title="` + title + `" style="color: ` + color + `;"><!--v:` + scope + `:` + itoa(textID) + `-->` + title + `<!--/v:` + scope + `:` + itoa(textID) + `--></p><!--/e:` + scope + `:` + itoa(fieldPlan.HydrationID) + `--><!--/r:` + scope + `:` + itoa(innerID) + `-->`
	}
	outerHTML := func(key string) string {
		scope := outerScope(key)
		return `<!--r:` + scope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `-->` + fieldHTML(key, "a", "Alpha", true, "red") + fieldHTML(key, "b", "Beta", false, "gray") + `<!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + scope + `:` + itoa(outerID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `-->` + outerHTML("row") + `<!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"row","fields":[{"id":"a","title":"Alpha","selected":true,"color":"red"},{"id":"b","title":"Beta","selected":false,"color":"gray"}]}]},"state":{},"incomplete":false,"awaitAt":{"` + outerScope("row") + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.__nestedClicks=0;window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{rows:[{id:"row",request:window.request,fields:[{id:"a",title:"Alpha",selected:true,color:"red"},{id:"b",title:"Beta",selected:false,color:"gray"}]}]});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html":             []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("nested settled hydration mutated DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".field")[0];window.b=document.querySelectorAll(".field")[1];window.b.click();window.resolveRequest("same")});await p.waitForFunction(()=>document.querySelectorAll(".field")[0]===window.a);if(await p.evaluate(()=>window.__nestedClicks)!==1)throw new Error("hydrated nested keyed row listener did not fire");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"row",request:window.request,fields:[{id:"b",title:"Beta 2",selected:true,color:"blue"},{id:"a",title:"Alpha",selected:false,color:"green"},{id:"c",title:"Gamma",selected:true,color:"purple"}]}]}));await p.waitForFunction(()=>Array.from(document.querySelectorAll(".field")).map(n=>n.textContent).join("|")==="Beta 2|Alpha|Gamma"&&document.querySelectorAll(".field")[0].getAttribute("title")==="Beta 2");const retained=await p.evaluate(()=>{window.b.click();return document.querySelectorAll(".field")[0]===window.b&&document.querySelectorAll(".field")[1]===window.a&&window.b.getAttribute("title")==="Beta 2"&&window.b.classList.contains("selected")&&window.b.style.color==="blue"&&!window.a.classList.contains("selected")&&window.a.style.color==="green"&&window.__nestedClicks===2});if(!retained)throw new Error("nested keyed rows lost identity, dynamic leaf values, or retained listener during reorder");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"row",request:window.request,fields:[{id:"b",title:"Beta 2",selected:true,color:"blue"},{id:"c",title:"Gamma",selected:true,color:"purple"}]}]})});await p.waitForFunction(()=>document.querySelectorAll(".field").length===2);if(await p.evaluate(()=>window.removed.isConnected))throw new Error("removed nested keyed row remained connected");await p.evaluate(()=>window.removed.click());if(await p.evaluate(()=>window.__nestedClicks)!==2)throw new Error("removed nested keyed row listener remained active");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedValueInput(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request;</script><template><main>{#each rows as row (row.id)}{#await request}<p class="pending">Pending</p>{:then value}{#each row.fields as field (field.id)}<input class="field" bind:value={field.title} />{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, ok := markers.eachID(outer.Span)
	if !ok {
		t.Fatal("outer keyed each marker is missing")
	}
	innerID, ok := markers.eachID(inner.Span)
	if !ok {
		t.Fatal("inner keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("nested await marker is missing")
	}
	mainPlan := plan.Nodes[0].(*SSRPlanElement)
	outerPlan := mainPlan.Children[0].(*SSRPlanEach)
	awaitPlan := outerPlan.Children[0].(*SSRPlanAwait)
	innerPlan := awaitPlan.ThenChildren[0].(*SSRPlanEach)
	inputPlan := innerPlan.Children[0].(*SSRPlanElement)
	outerScope := func(key string) string { return "vela-page--each-" + itoa(outer.Span.Start) + "-" + key }
	innerScope := func(outerKey, fieldKey string) string {
		return outerScope(outerKey) + "--each-" + itoa(inner.Span.Start) + "-" + fieldKey
	}
	inputHTML := func(outerKey, fieldKey, value string) string {
		scope := innerScope(outerKey, fieldKey)
		return `<!--r:` + scope + `:` + itoa(innerID) + `--><!--e:` + scope + `:` + itoa(inputPlan.HydrationID) + `--><input vela-page="" class="field" value="` + value + `"><!--/e:` + scope + `:` + itoa(inputPlan.HydrationID) + `--><!--/r:` + scope + `:` + itoa(innerID) + `-->`
	}
	rowScope := outerScope("row")
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + rowScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `-->` + inputHTML("row", "a", "Server") + `<!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + rowScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",title:"Server"}]}];window.__request=new Promise(()=>{});const input=document.querySelector(".field");input.value="Edited before hydration";window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.input=input</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html":             []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({writes:window.initial,value:window.input.value,model:window.__rows[0].fields[0].title}));if(initial.writes!==0||initial.value!=="Edited before hydration"||initial.model!=="Edited before hydration")throw new Error("input hydration did not preserve browser value: "+JSON.stringify(initial));await p.evaluate(()=>{window.input.value="Typed";window.input.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>window.__rows[0].fields[0].title==="Typed");await p.evaluate(()=>window.hydrated.component.destroy());await p.evaluate(()=>{window.input.value="After destroy";window.input.dispatchEvent(new Event("input",{bubbles:true}))});if(await p.evaluate(()=>window.__rows[0].fields[0].title)!=="Typed")throw new Error("destroyed input listener remained active");if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed await input snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed await input snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedCheckedInput(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<input class="field" type="checkbox" bind:checked={field.done} />{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested checkbox marker is missing")
	}
	mainPlan := plan.Nodes[0].(*SSRPlanElement)
	inputPlan := mainPlan.Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(inputPlan.HydrationID) + `--><input vela-page="" class="field" type="checkbox"><!--/e:` + innerScope + `:` + itoa(inputPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",done:false}]}];window.__request=new Promise(()=>{});const input=document.querySelector(".field");input.checked=true;window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.input=input</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({writes:window.initial,checked:window.input.checked,model:window.__rows[0].fields[0].done}));if(initial.writes!==0||!initial.checked||!initial.model)throw new Error("checkbox hydration did not preserve browser value: "+JSON.stringify(initial));await p.evaluate(()=>{window.input.checked=false;window.input.dispatchEvent(new Event("change",{bubbles:true}))});await p.waitForFunction(()=>window.__rows[0].fields[0].done===false);await p.evaluate(()=>window.hydrated.component.destroy());await p.evaluate(()=>{window.input.checked=true;window.input.dispatchEvent(new Event("change",{bubbles:true}))});if(await p.evaluate(()=>window.__rows[0].fields[0].done)!==false)throw new Error("destroyed checkbox listener remained active");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed await checkbox snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed await checkbox snapshot browser output=%q", got)
	}
}

func TestSSRHydrationKeyedEachAwaitNestedTextareaUsesControlRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<textarea bind:value={field.title} />{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"createElement('textarea')", "addEventListener('input'", "removeEventListener('input'", ".value"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested textarea control lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesControlRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><option value="one">One</option><option value="two">Two</option></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"createElement('select')", "addEventListener('change'", "removeEventListener('change'", "$$insertHTML($$control, '<option"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested select control lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesTypedOptionTextRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><option value="one">{field.label}</option></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"options.item(0)", "textContent", "$$inner_option_update", "$$initial_option"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested select OptionText lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesTypedOptionDisabledRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><option value="one" :disabled={field.disabled}>One</option><option value="two">Two</option></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"options.item(0)", ".disabled", "$$inner_option_dynamic_update", "$$initial_option_dynamic"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested select OptionDynamic lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesTypedOptionValueRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><option :value={field.optionValue}>One</option><option value="two">Two</option></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"options.item(0)", "$$inner_option_dynamic.value", "$$initial_option_dynamic", "$$inner_control_update"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested select option-value lowering misses %q:\n%s", want, code)
		}
	}
	if strings.Index(code, "$$inner_option_dynamic_update") > strings.Index(code, "$$inner_control_update") {
		t.Fatalf("option value writer must run before select value writer:\n%s", code)
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesTypedOptgroupLabelRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup :label={field.groupLabel}><option value="one">One</option></optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"querySelectorAll('optgroup')", "$$inner_option_group_update", ".label", "$$initial_option_group"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested select optgroup-label lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesUnkeyedOptionListRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}>{#each field.options as option}<option :value={option.value}>{option.label}</option>{/each}</select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"createDocumentFragment", "replaceChildren", "$$inner_option_list_update", "$$option_element.value"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested unkeyed option-list lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesUnkeyedOptionListFallbackRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}>{#each field.options as option}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$insertHTML($$option_fragment, '<option", "replaceChildren", "$$inner_option_list_update"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested unkeyed option-list fallback lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesOptgroupOptionListRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup label="Available">{#each field.options as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"querySelectorAll('optgroup')", "createElement('optgroup')", "$$initial_option_group", "replaceChildren"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested optgroup option-list lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesKeyedOptionListRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$reconcileHydrationKeyedElements", "$$inner_option_blocks", "duplicate key in nested option list", "$$option_element.value"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed option-list lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesKeyedOptionListFallbackRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$inner_option_fallback", "$$reconcileHydrationKeyedElements", "replaceChildren", "$$insertHTML($$option_fragment, '<option", "$$inner_option_blocks"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed option-list fallback lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesKeyedOptgroupOptionListRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup label="Available">{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"querySelectorAll('optgroup')", "querySelectorAll('option')", "$$reconcileHydrationKeyedElements", "$$inner_option_blocks", "createElement('optgroup')"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed optgroup option-list lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectRecognizesTwoOwnerRecipe(t *testing.T) {
	nodes, err := parseTemplate(`<main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup label="Current">{#each field.current as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="History">{#each field.history as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main>`)
	if err != nil {
		t.Fatal(err)
	}
	main := nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	nested := await.ThenChildren[0].(*EachBlockNode)
	element, bind, lists, ok := hydrationKeyedAwaitNestedMultiSelectOptionLists(nested)
	if !ok || element == nil || bind == nil {
		t.Fatal("two keyed optgroup owners were not recognized")
	}
	if bind.Name != "value" || len(lists) != 2 || lists[0].GroupIndex != 0 || lists[1].GroupIndex != 1 {
		t.Fatalf("unexpected two-owner recipe: bind=%q lists=%#v", bind.Name, lists)
	}
	for index, list := range lists {
		if list.Kind != hydrationKeyedEachSelectOptionListKeyed || list.Key == nil || len(list.Fallback) != 0 {
			t.Fatalf("owner %d is not a strict keyed no-fallback list: %#v", index, list)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectRejectsUnsafeTwoOwnerRecipe(t *testing.T) {
	nodes, err := parseTemplate(`<main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup :label={field.currentLabel}>{#each field.current as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="History">{#each field.history as option (option.id)}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</optgroup></select>{/each}{/await}{/each}</main>`)
	if err != nil {
		t.Fatal(err)
	}
	main := nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	nested := await.ThenChildren[0].(*EachBlockNode)
	if _, _, _, ok := hydrationKeyedAwaitNestedMultiSelectOptionLists(nested); ok {
		t.Fatal("dynamic labels or fallback branches must not enter the first multi-owner recipe")
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesKeyedOptgroupOptionListFallbackRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup label="Available">{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"querySelectorAll('optgroup')", "querySelectorAll('option')", "$$inner_option_fallback", "$$reconcileHydrationKeyedElements", "createElement('optgroup')"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed optgroup option-list fallback lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesDynamicKeyedOptgroupOptionListRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup :label={field.groupLabel}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$inner_option_group_update", "$$reconcileHydrationKeyedElements", "$$initial_option_group_dynamic", "querySelectorAll('optgroup')", "createElement('optgroup')"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested dynamic keyed optgroup option-list lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesDynamicKeyedOptgroupOptionListFallbackRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup :label={field.groupLabel}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$inner_option_group_update", "$$inner_option_fallback", "$$reconcileHydrationKeyedElements", "$$initial_option_group_dynamic", "replaceChildren"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested dynamic keyed optgroup option-list fallback lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesMixedOptgroupOptionTreeRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup label="Archived"><option value="archived">Archived</option></optgroup><optgroup :label={field.groupLabel}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$initial_option_group_0", "$$initial_option_group_1", "Archived", "$$inner_option_group_update", "$$reconcileHydrationKeyedElements", "item(1)"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested mixed optgroup option-tree lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedSelectUsesDynamicSiblingOptgroupRecipe(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let request = Promise.resolve();</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select bind:value={field.choice}><optgroup :label={field.archivedLabel}><option value="archived">Archived</option></optgroup><optgroup label="Available">{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$inner_option_group_update", "item(0)", "item(1)", "$$initial_option_group_0", "$$initial_option_group_1", "$$reconcileHydrationKeyedElements"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested dynamic sibling optgroup lowering misses %q:\n%s", want, code)
		}
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedUnkeyedOptionList(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request; window.__velaRows = rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}>{#each field.options as option}<option :value={option.value}>{option.label}</option>{/each}</select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested option-list marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><option value="one">One</option><option value="two" selected>Two</option></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"two",options:[{value:"one",label:"One"},{value:"two",label:"Two"}]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({writes:window.initial,options:Array.from(window.field.options).map(x=>x.value+":"+x.text).join("|"),value:window.field.value}));if(initial.writes!==0||initial.options!=="one:One|two:Two"||initial.value!=="two")throw new Error("option-list hydration changed SSR DOM: "+JSON.stringify(initial));await p.evaluate(()=>{const field=window.__velaRows[0].fields[0];field.options=[{value:"three",label:"Three"},{value:"four",label:"Four"}];field.choice="three"});await p.waitForFunction(()=>Array.from(document.querySelector(".field")?.options||[]).map(x=>x.value+":"+x.text).join("|")==="three:Three|four:Four"&&document.querySelector(".field")?.value==="three");if(!(await p.evaluate(()=>window.field===document.querySelector(".field"))))throw new Error("select was recreated");await p.evaluate(()=>window.hydrated.component.destroy());await p.evaluate(()=>{window.field.value="four";window.field.dispatchEvent(new Event("change",{bubbles:true}))});if(await p.evaluate(()=>window.__velaRows[0].fields[0].choice)!=="three")throw new Error("destroyed listener remained active");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested unkeyed option-list browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested unkeyed option-list browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedUnkeyedOptionListFallback(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request; window.__velaRows = rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}>{#each field.options as option}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested option-list fallback marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><option value="" selected>None</option></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"",options:[]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("fallback adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[{value:"one",label:"One"}];f.choice="one"});await p.waitForFunction(()=>document.querySelector(".field")?.value==="one"&&document.querySelector(".field")?.options[0].text==="One");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[];f.choice=""});await p.waitForFunction(()=>document.querySelector(".field")?.options[0].text==="None"&&document.querySelector(".field")?.value==="");if(!(await p.evaluate(()=>window.field===document.querySelector(".field"))))throw new Error("fallback transition recreated select");await p.evaluate(()=>window.hydrated.component.destroy());process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested option-list fallback browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested option-list fallback browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedOptionListFallback(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows=$state.deep(window.__rows);let request=window.__request;window.__velaRows=rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested keyed option-list fallback marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><option value="" selected>None</option></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"",options:[]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("keyed fallback adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[{id:"one",value:"one",label:"One"},{id:"two",value:"two",label:"Two"}];f.choice="two"});await p.waitForFunction(()=>Array.from(document.querySelector(".field")?.options||[]).map(x=>x.value+":"+x.text).join("|")==="one:One|two:Two"&&document.querySelector(".field")?.value==="two");await p.evaluate(()=>{window.two=document.querySelector(".field").options[1];const f=window.__velaRows[0].fields[0];f.options=[{id:"two",value:"two",label:"Two updated"},{id:"three",value:"three",label:"Three"}];f.choice="two"});await p.waitForFunction(()=>Array.from(document.querySelector(".field")?.options||[]).map(x=>x.value+":"+x.text).join("|")==="two:Two updated|three:Three");if(!(await p.evaluate(()=>document.querySelector(".field").options[0]===window.two)))throw new Error("keyed fallback transition lost retained option identity");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[];f.choice=""});await p.waitForFunction(()=>document.querySelector(".field")?.options.length===1&&document.querySelector(".field")?.options[0].text==="None"&&document.querySelector(".field")?.value==="");if(!(await p.evaluate(()=>window.field===document.querySelector(".field"))))throw new Error("keyed fallback transition recreated select");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed option-list fallback browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed option-list fallback browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedOptgroupOptionListFallback(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows=$state.deep(window.__rows);let request=window.__request;window.__velaRows=rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><optgroup label="Available">{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested keyed optgroup option-list fallback marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><optgroup label="Available"><option value="" selected>None</option></optgroup></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"",options:[]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field");window.group=window.field.querySelector("optgroup")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("keyed optgroup fallback adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[{id:"one",value:"one",label:"One"},{id:"two",value:"two",label:"Two"}];f.choice="two"});await p.waitForFunction(()=>Array.from(document.querySelector(".field optgroup")?.querySelectorAll("option")||[]).map(x=>x.value+":"+x.text).join("|")==="one:One|two:Two"&&document.querySelector(".field")?.value==="two");await p.evaluate(()=>{window.two=document.querySelector(".field optgroup").querySelectorAll("option")[1];const f=window.__velaRows[0].fields[0];f.options=[{id:"two",value:"two",label:"Two updated"},{id:"three",value:"three",label:"Three"}];f.choice="two"});await p.waitForFunction(()=>Array.from(document.querySelector(".field optgroup")?.querySelectorAll("option")||[]).map(x=>x.value+":"+x.text).join("|")==="two:Two updated|three:Three");const retained=await p.evaluate(()=>{const group=document.querySelector(".field optgroup");return window.field===document.querySelector(".field")&&window.group===group&&group.querySelectorAll("option")[0]===window.two});if(!retained)throw new Error("keyed optgroup fallback transition lost owner or retained option identity");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[];f.choice=""});await p.waitForFunction(()=>document.querySelector(".field optgroup")?.querySelectorAll("option").length===1&&document.querySelector(".field optgroup option")?.text==="None"&&document.querySelector(".field")?.value==="");if(!(await p.evaluate(()=>window.field===document.querySelector(".field")&&window.group===document.querySelector(".field optgroup"))))throw new Error("keyed optgroup fallback transition recreated owner");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed optgroup option-list fallback browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed optgroup option-list fallback browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedOptgroupOptionList(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows=$state.deep(window.__rows);let request=window.__request;window.__velaRows=rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><optgroup label="Available">{#each field.options as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested optgroup option-list marker missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><optgroup label="Available"><option value="one">One</option><option value="two" selected>Two</option></optgroup></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"two",options:[{value:"one",label:"One"},{value:"two",label:"Two"}]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field");window.group=window.field.querySelector("optgroup")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("optgroup adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[{value:"three",label:"Three"}];f.choice="three"});await p.waitForFunction(()=>document.querySelector(".field")?.value==="three"&&document.querySelector(".field optgroup option")?.text==="Three");if(!(await p.evaluate(()=>window.field===document.querySelector(".field")&&window.group===document.querySelector(".field optgroup"))))throw new Error("optgroup owner was recreated");await p.evaluate(()=>window.hydrated.component.destroy());process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested optgroup option-list browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested optgroup option-list browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedDynamicKeyedOptgroupOptionList(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows=$state.deep(window.__rows);let request=window.__request;window.__velaRows=rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><optgroup :label={field.groupLabel}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested dynamic keyed optgroup option-list marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><optgroup label="Available"><option value="one">One</option><option value="two" selected>Two</option></optgroup></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",groupLabel:"Available",choice:"two",options:[{id:"one",value:"one",label:"One"},{id:"two",value:"two",label:"Two"}]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field");window.group=window.field.querySelector("optgroup");window.two=window.group.querySelectorAll("option")[1]</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("dynamic keyed optgroup adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.groupLabel="Updated";f.options=[{id:"two",value:"two",label:"Two updated"},{id:"three",value:"three",label:"Three"}];f.choice="two"});await p.waitForFunction(()=>{const group=document.querySelector(".field optgroup");return group?.label==="Updated"&&Array.from(group.querySelectorAll("option")).map(x=>x.value+":"+x.text).join("|")==="two:Two updated|three:Three"&&document.querySelector(".field")?.value==="two"});const retained=await p.evaluate(()=>{const group=document.querySelector(".field optgroup");return window.field===document.querySelector(".field")&&window.group===group&&group.querySelectorAll("option")[0]===window.two});if(!retained)throw new Error("dynamic keyed optgroup update lost owner or retained option identity");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested dynamic keyed optgroup option-list browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested dynamic keyed optgroup option-list browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedDynamicKeyedOptgroupOptionListFallback(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows=$state.deep(window.__rows);let request=window.__request;window.__velaRows=rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><optgroup :label={field.groupLabel}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{:else}<option value="">None</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested dynamic keyed optgroup option-list fallback marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><optgroup label="Available"><option value="" selected>None</option></optgroup></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",groupLabel:"Available",choice:"",options:[]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field");window.group=window.field.querySelector("optgroup")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("dynamic keyed optgroup fallback adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.groupLabel="Updated";f.options=[{id:"one",value:"one",label:"One"},{id:"two",value:"two",label:"Two"}];f.choice="two"});await p.waitForFunction(()=>{const group=document.querySelector(".field optgroup");return group?.label==="Updated"&&Array.from(group.querySelectorAll("option")).map(x=>x.value+":"+x.text).join("|")==="one:One|two:Two"&&document.querySelector(".field")?.value==="two"});await p.evaluate(()=>{window.two=document.querySelector(".field optgroup").querySelectorAll("option")[1];const f=window.__velaRows[0].fields[0];f.groupLabel="Changed";f.options=[{id:"two",value:"two",label:"Two updated"},{id:"three",value:"three",label:"Three"}];f.choice="two"});await p.waitForFunction(()=>{const group=document.querySelector(".field optgroup");return group?.label==="Changed"&&Array.from(group.querySelectorAll("option")).map(x=>x.value+":"+x.text).join("|")==="two:Two updated|three:Three"});const retained=await p.evaluate(()=>{const group=document.querySelector(".field optgroup");return window.field===document.querySelector(".field")&&window.group===group&&group.querySelectorAll("option")[0]===window.two});if(!retained)throw new Error("dynamic keyed optgroup fallback transition lost owner or retained option identity");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.groupLabel="Empty";f.options=[];f.choice=""});await p.waitForFunction(()=>{const group=document.querySelector(".field optgroup");return group?.label==="Empty"&&group.querySelectorAll("option").length===1&&group.querySelector("option")?.text==="None"&&document.querySelector(".field")?.value===""});if(!(await p.evaluate(()=>window.field===document.querySelector(".field")&&window.group===document.querySelector(".field optgroup"))))throw new Error("dynamic keyed optgroup fallback transition recreated owner");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested dynamic keyed optgroup option-list fallback browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested dynamic keyed optgroup option-list fallback browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedMixedOptgroupOptionTree(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows=$state.deep(window.__rows);let request=window.__request;window.__velaRows=rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><optgroup label="Archived"><option value="archived">Archived</option></optgroup><optgroup :label={field.groupLabel}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested mixed optgroup option-tree marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><optgroup label="Archived"><option value="archived">Archived</option></optgroup><optgroup label="Available"><option value="one">One</option><option value="two" selected>Two</option></optgroup></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",groupLabel:"Available",choice:"two",options:[{id:"one",value:"one",label:"One"},{id:"two",value:"two",label:"Two"}]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field");window.archived=window.field.querySelectorAll("optgroup")[0];window.available=window.field.querySelectorAll("optgroup")[1];window.two=window.available.querySelectorAll("option")[1]</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("mixed optgroup adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.groupLabel="Current";f.options=[{id:"two",value:"two",label:"Two updated"},{id:"three",value:"three",label:"Three"}];f.choice="two"});await p.waitForFunction(()=>{const groups=document.querySelector(".field").querySelectorAll("optgroup");return groups[0]?.label==="Archived"&&groups[0]?.querySelector("option")?.value==="archived"&&groups[1]?.label==="Current"&&Array.from(groups[1].querySelectorAll("option")).map(x=>x.value+":"+x.text).join("|")==="two:Two updated|three:Three"&&document.querySelector(".field")?.value==="two"});const retained=await p.evaluate(()=>{const groups=document.querySelector(".field").querySelectorAll("optgroup");return window.field===document.querySelector(".field")&&window.archived===groups[0]&&window.available===groups[1]&&groups[1].querySelectorAll("option")[0]===window.two});if(!retained)throw new Error("mixed optgroup update touched a static sibling or lost retained option identity");await p.evaluate(()=>window.__velaRows[0].fields.push({id:"b",groupLabel:"New",choice:"new",options:[{id:"new",value:"new",label:"New option"}]}));await p.waitForFunction(()=>{const fields=document.querySelectorAll(".field");if(fields.length!==2)return false;const groups=fields[1].querySelectorAll("optgroup");return groups.length===2&&groups[0].label==="Archived"&&groups[0].querySelector("option")?.value==="archived"&&groups[1].label==="New"&&groups[1].querySelector("option")?.value==="new"&&fields[1].value==="new"});await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested mixed optgroup option-tree browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested mixed optgroup option-tree browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedDynamicSiblingOptgroup(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows=$state.deep(window.__rows);let request=window.__request;window.__velaRows=rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><optgroup :label={field.archivedLabel}><option value="archived">Archived</option></optgroup><optgroup label="Available">{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested dynamic sibling optgroup marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><optgroup label="Archived"><option value="archived">Archived</option></optgroup><optgroup label="Available"><option value="one">One</option><option value="two" selected>Two</option></optgroup></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",archivedLabel:"Archived",choice:"two",options:[{id:"one",value:"one",label:"One"},{id:"two",value:"two",label:"Two"}]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field");window.archived=window.field.querySelectorAll("optgroup")[0];window.available=window.field.querySelectorAll("optgroup")[1];window.two=window.available.querySelectorAll("option")[1]</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("dynamic sibling optgroup adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.archivedLabel="Past";f.options=[{id:"two",value:"two",label:"Two updated"},{id:"three",value:"three",label:"Three"}];f.choice="two"});await p.waitForFunction(()=>{const groups=document.querySelector(".field").querySelectorAll("optgroup");return groups[0]?.label==="Past"&&groups[0]?.querySelector("option")?.value==="archived"&&groups[1]?.label==="Available"&&Array.from(groups[1].querySelectorAll("option")).map(x=>x.value+":"+x.text).join("|")==="two:Two updated|three:Three"});const retained=await p.evaluate(()=>{const groups=document.querySelector(".field").querySelectorAll("optgroup");return window.archived===groups[0]&&window.available===groups[1]&&groups[1].querySelectorAll("option")[0]===window.two});if(!retained)throw new Error("dynamic sibling update lost group or option identity");await p.evaluate(()=>window.__velaRows[0].fields.push({id:"b",archivedLabel:"Older",choice:"new",options:[{id:"new",value:"new",label:"New option"}]}));await p.waitForFunction(()=>{const fields=document.querySelectorAll(".field");if(fields.length!==2)return false;const groups=fields[1].querySelectorAll("optgroup");return groups.length===2&&groups[0].label==="Older"&&groups[0].querySelector("option")?.value==="archived"&&groups[1].label==="Available"&&groups[1].querySelector("option")?.value==="new"&&fields[1].value==="new"});await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested dynamic sibling optgroup browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested dynamic sibling optgroup browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedOptgroupOptionList(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows=$state.deep(window.__rows);let request=window.__request;window.__velaRows=rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><optgroup label="Available">{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested keyed optgroup option-list marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><optgroup label="Available"><option value="one">One</option><option value="two" selected>Two</option></optgroup></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"two",options:[{id:"one",value:"one",label:"One"},{id:"two",value:"two",label:"Two"}]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field");window.group=window.field.querySelector("optgroup");window.one=window.group.querySelectorAll("option")[0];window.two=window.group.querySelectorAll("option")[1]</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("keyed optgroup adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[{id:"two",value:"two",label:"Two updated"},{id:"three",value:"three",label:"Three"}];f.choice="two"});await p.waitForFunction(()=>Array.from(document.querySelector(".field optgroup")?.querySelectorAll("option")||[]).map(x=>x.value+":"+x.text).join("|")==="two:Two updated|three:Three");const retained=await p.evaluate(()=>{const group=document.querySelector(".field optgroup");return window.field===document.querySelector(".field")&&window.group===group&&group.querySelectorAll("option")[0]===window.two&&window.one.isConnected===false&&document.querySelector(".field").value==="two"});if(!retained)throw new Error("keyed optgroup reconciliation lost owner, identity, removal, or selection");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed optgroup option-list browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed optgroup option-list browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedOptionList(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request; window.__velaRows = rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}>{#each field.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested keyed option-list marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><option value="one">One</option><option value="two" selected>Two</option></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"two",options:[{id:"one",value:"one",label:"One"},{id:"two",value:"two",label:"Two"}]}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field");window.one=window.field.options[0];window.two=window.field.options[1]</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if((await p.evaluate(()=>window.initial))!==0)throw new Error("keyed option adoption mutated SSR DOM");await p.evaluate(()=>{const f=window.__velaRows[0].fields[0];f.options=[{id:"two",value:"two",label:"Two updated"},{id:"three",value:"three",label:"Three"}];f.choice="two"});await p.waitForFunction(()=>Array.from(document.querySelector(".field")?.options||[]).map(x=>x.value+":"+x.text).join("|")==="two:Two updated|three:Three");const retained=await p.evaluate(()=>document.querySelector(".field").options[0]===window.two&&window.one.isConnected===false&&document.querySelector(".field").value==="two");if(!retained)throw new Error("keyed option reconciliation lost identity, removal, or selection");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed option-list browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed option-list browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedSelectOptionText(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request; window.__velaRows = rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p class="pending">Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><option value="one">{field.label}</option><option value="two">Two</option></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested select option text marker is missing")
	}
	mainPlan := plan.Nodes[0].(*SSRPlanElement)
	selectPlan := mainPlan.Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	optionPlan := selectPlan.Children[0].(*SSRPlanElement)
	optionTextPlan := optionPlan.Children[0].(*SSRPlanText)
	textID := optionTextPlan.Parts[0].DynamicID
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><option value="one"><!--v:` + innerScope + `:` + itoa(textID) + `-->Server<!--/v:` + innerScope + `:` + itoa(textID) + `--></option><option value="two">Two</option></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"one",label:"Server"}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({writes:window.initial,label:window.field.options[0].textContent,value:window.field.value}));if(initial.writes!==0||initial.label!=="Server"||initial.value!=="one")throw new Error("select option-text hydration mutated or lost SSR state: "+JSON.stringify(initial));await p.evaluate(()=>window.__velaRows[0].fields[0].label="Updated");await p.waitForFunction(()=>document.querySelector(".field")?.options[0].textContent==="Updated");const retained=await p.evaluate(()=>document.querySelector(".field")===window.field);if(!retained)throw new Error("retained nested select row was recreated");await p.evaluate(()=>window.hydrated.component.destroy());await p.evaluate(()=>{window.field.value="two";window.field.dispatchEvent(new Event("change",{bubbles:true}))});if(await p.evaluate(()=>window.__velaRows[0].fields[0].choice)!=="one")throw new Error("destroyed select listener remained active");if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed await select option text browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed await select option text browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedSelectOptionDisabled(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request; window.__velaRows = rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p class="pending">Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><option value="one" :disabled={field.disabled}>One</option><option value="two">Two</option></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested select option disabled marker is missing")
	}
	mainPlan := plan.Nodes[0].(*SSRPlanElement)
	selectPlan := mainPlan.Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><option value="one" disabled>One</option><option value="two">Two</option></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"two",disabled:true}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({writes:window.initial,disabled:window.field.options[0].disabled,value:window.field.value}));if(initial.writes!==0||!initial.disabled||initial.value!=="two")throw new Error("select option-disabled hydration mutated or lost selection: "+JSON.stringify(initial));await p.evaluate(()=>window.__velaRows[0].fields[0].disabled=false);await p.waitForFunction(()=>document.querySelector(".field")?.options[0].disabled===false);const retained=await p.evaluate(()=>document.querySelector(".field")===window.field&&window.field.value==="two");if(!retained)throw new Error("retained nested select row lost identity or selection");await p.evaluate(()=>window.hydrated.component.destroy());await p.evaluate(()=>{window.field.value="one";window.field.dispatchEvent(new Event("change",{bubbles:true}))});if(await p.evaluate(()=>window.__velaRows[0].fields[0].choice)!=="two")throw new Error("destroyed select listener remained active");if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed await select option disabled browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed await select option disabled browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedSelectOptionValue(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request; window.__velaRows = rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><option :value={field.optionValue}>One</option><option value="two">Two</option></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested select option value marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><option value="server" selected>One</option><option value="two">Two</option></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"server",optionValue:"server"}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({writes:window.initial,value:window.field.value,option:window.field.options[0].value}));if(initial.writes!==0||initial.value!=="server"||initial.option!=="server")throw new Error("option-value hydration lost SSR selection: "+JSON.stringify(initial));await p.evaluate(()=>{const field=window.__velaRows[0].fields[0];field.optionValue="updated";field.choice="updated"});await p.waitForFunction(()=>document.querySelector(".field")?.value==="updated"&&document.querySelector(".field")?.options[0].value==="updated");if(!(await p.evaluate(()=>document.querySelector(".field")===window.field)))throw new Error("retained nested select row was recreated");await p.evaluate(()=>window.hydrated.component.destroy());await p.evaluate(()=>{window.field.value="two";window.field.dispatchEvent(new Event("change",{bubbles:true}))});if(await p.evaluate(()=>window.__velaRows[0].fields[0].choice)!=="updated")throw new Error("destroyed select listener remained active");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed await select option value browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed await select option value browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitAdoptsNestedKeyedOptgroupLabel(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows); let request = window.__request; window.__velaRows = rows;</script><template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select class="field" bind:value={field.choice}><optgroup :label={field.groupLabel}><option value="one">One</option></optgroup><optgroup label="Other"><option value="two">Two</option></optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	outer := main.Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	outerID, outerOK := markers.eachID(outer.Span)
	innerID, innerOK := markers.eachID(inner.Span)
	awaitID, awaitOK := markers.awaitID(await.Span)
	if !outerOK || !innerOK || !awaitOK {
		t.Fatal("nested optgroup label marker is missing")
	}
	selectPlan := plan.Nodes[0].(*SSRPlanElement).Children[0].(*SSRPlanEach).Children[0].(*SSRPlanAwait).ThenChildren[0].(*SSRPlanEach).Children[0].(*SSRPlanElement)
	outerScope := "vela-page--each-" + itoa(outer.Span.Start) + "-row"
	innerScope := outerScope + "--each-" + itoa(inner.Span.Start) + "-a"
	html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(outerID) + `--><!--r:` + outerScope + `:` + itoa(outerID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><!--v:vela-page:` + itoa(innerID) + `--><!--r:` + innerScope + `:` + itoa(innerID) + `--><!--e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><select vela-page="" class="field"><optgroup label="Server"><option value="one">One</option></optgroup><optgroup label="Other"><option value="two" selected>Two</option></optgroup></select><!--/e:` + innerScope + `:` + itoa(selectPlan.HydrationID) + `--><!--/r:` + innerScope + `:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(innerID) + `--><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + outerScope + `:` + itoa(outerID) + `--><!--/v:vela-page:` + itoa(outerID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":[],"incomplete":false,"awaitAt":{"` + outerScope + `:` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">window.__rows=[{id:"row",fields:[{id:"a",choice:"two",groupLabel:"Server"}]}];window.__request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});import hydratePage from "./Page.hydrate.js";window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length;window.field=document.querySelector(".field")</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({writes:window.initial,label:window.field.querySelector("optgroup").label,value:window.field.value}));if(initial.writes!==0||initial.label!=="Server"||initial.value!=="two")throw new Error("optgroup hydration mutated or lost SSR selection: "+JSON.stringify(initial));await p.evaluate(()=>window.__velaRows[0].fields[0].groupLabel="Updated");await p.waitForFunction(()=>document.querySelector(".field optgroup")?.label==="Updated");if(!(await p.evaluate(()=>document.querySelector(".field")===window.field&&window.field.value==="two")))throw new Error("retained select lost identity or selection");await p.evaluate(()=>window.hydrated.component.destroy());await p.evaluate(()=>{window.field.value="one";window.field.dispatchEvent(new Event("change",{bubbles:true}))});if(await p.evaluate(()=>window.__velaRows[0].fields[0].choice)!=="two")throw new Error("destroyed select listener remained active");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed await optgroup label browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed await optgroup label browser output=%q", got)
	}
}

func TestBrowserSSRHydrationDeepMutationRefreshesOnlyNestedKeyedRows(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows || []); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.fields as field (field.id)}{#if field.visible}<button class="field" :data-id={field.id} @click={() => selected = field.title}>{field.title}</button>{:else}<input class="hidden-input" bind:value={field.title} /><button class="hidden" :data-id={field.id} @click={() => selected = "hidden"}>Hidden</button>{/if}{:else}<p class="empty" :data-selected={selected}>No fields {selected}</p><button class="empty-action" @click={() => selected = "empty"}>Set empty</button>{/each}<button class="toggle" @click={() => rows[0].fields[0].visible = !rows[0].fields[0].visible}>Toggle</button><button class="shuffle" @click={() => rows[0].fields = [{id: "b", title: "B*", visible: true}, {id: "a", title: "A", visible: true}, {id: "c", title: "C", visible: true}]}>Shuffle</button><button class="remove" @click={() => rows[0].fields = [{id: "b", title: "B*", visible: true}, {id: "c", title: "C", visible: true}]}>Remove</button><button class="empty-list" @click={() => rows[0].fields = []}>Empty</button><p class="selected">{selected}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	rowScope := "vela-page--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><p vela-page="" class="pending">Loading</p><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	serverHTML := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-page:` + itoa(eachID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":["rows"],"incomplete":false}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.__rows=[{id:"a",request:window.promise,fields:[{id:"a",title:"A",visible:true},{id:"b",title:"B",visible:true}]}];window.__observer=new MutationObserver(()=>{});window.__observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body);window.__initialMutations=window.__observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.__initialMutations)!==0)throw new Error("pending keyed-await hydration mutated SSR DOM");await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelectorAll(".field").length===2);await p.evaluate(()=>{window.a=document.querySelector("[data-id=a]");window.b=document.querySelector("[data-id=b]");document.querySelector(".shuffle").click()});await p.waitForFunction(()=>Array.from(document.querySelectorAll(".field")).map(x=>x.dataset.id+":"+x.textContent).join("|")==="b:B*|a:A|c:C");if(!(await p.evaluate(()=>document.querySelector("[data-id=a]")===window.a&&document.querySelector("[data-id=b]")===window.b)))throw new Error("deep mutation replaced retained nested rows");await p.evaluate(()=>document.querySelector("[data-id=b]").click());await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="B*");await p.evaluate(()=>document.querySelector(".remove").click());await p.waitForFunction(()=>document.querySelectorAll(".field").length===2&&!document.querySelector("[data-id=a]"));await p.evaluate(async()=>{window.a.click();await new Promise(resolve=>setTimeout(resolve,0));window.value=document.querySelector("p.selected").textContent});if((await p.evaluate(()=>window.value))!=="B*")throw new Error("removed nested row listener survived deep mutation");await p.evaluate(()=>document.querySelector(".empty-list").click());await p.waitForFunction(()=>document.querySelectorAll(".field").length===0&&document.querySelector(".empty")?.textContent==="No fields B*"&&document.querySelector(".empty")?.dataset.selected==="B*");await p.evaluate(()=>{window.emptyAction=document.querySelector(".empty-action");window.emptyAction.click()});await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="empty"&&document.querySelector(".empty")?.textContent==="No fields empty");await p.evaluate(()=>document.querySelector(".shuffle").click());await p.waitForFunction(()=>document.querySelectorAll(".field").length===3&&!document.querySelector(".empty"));await p.evaluate(()=>document.querySelector("[data-id=b]").click());await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="B*");await p.evaluate(async()=>{window.emptyAction.click();await new Promise(resolve=>setTimeout(resolve,0));window.emptyValue=document.querySelector("p.selected").textContent});if((await p.evaluate(()=>window.emptyValue))!=="B*")throw new Error("removed empty branch listener survived");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	beforeConditional := `await p.waitForFunction(()=>document.querySelectorAll(".field").length===2);await p.evaluate(()=>{window.a=document.querySelector("[data-id=a]");window.b=document.querySelector("[data-id=b]");document.querySelector(".shuffle").click()});`
	afterConditional := `await p.waitForFunction(()=>document.querySelectorAll(".field").length===2);await p.evaluate(()=>{document.querySelector(".toggle").click()});await p.waitForFunction(()=>document.querySelector(".hidden")?.dataset.id==="a"&&document.querySelector(".hidden-input"));await p.evaluate(()=>{window.hidden=document.querySelector(".hidden");window.hiddenInput=document.querySelector(".hidden-input");window.hiddenInput.value="Edited";window.hiddenInput.dispatchEvent(new Event("input",{bubbles:true}));window.hidden.click();document.querySelector(".toggle").click()});await p.waitForFunction(()=>document.querySelectorAll(".field").length===2&&document.querySelector("p.selected")?.textContent==="hidden"&&document.querySelector("[data-id=a]")?.textContent==="Edited");await p.evaluate(async()=>{window.hidden.click();window.hiddenInput.value="stale";window.hiddenInput.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.afterHidden=document.querySelector("p.selected").textContent;window.afterInput=document.querySelector("[data-id=a]").textContent});if((await p.evaluate(()=>window.afterHidden))!=="hidden"||(await p.evaluate(()=>window.afterInput))!=="Edited")throw new Error("conditional listener or binding survived branch replacement");await p.evaluate(()=>{window.a=document.querySelector("[data-id=a]");window.b=document.querySelector("[data-id=b]");document.querySelector(".shuffle").click()});`
	if !strings.Contains(browser, beforeConditional) {
		t.Fatal("conditional browser assertion insertion point is missing")
	}
	browserScript := strings.Replace(browser, beforeConditional, afterConditional, 1)
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("deep nested keyed browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("deep nested keyed browser output=%q", got)
	}
}

func TestSSRHydrationKeyedEachAwaitNestedEachBindings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attr  string
		state string
	}{
		{name: "value", attr: "bind:value={draft}", state: "draft"},
		{name: "checked", attr: "bind:checked={checked}", state: "checked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `<script>export let rows = []; let draft = $state(""); let checked = $state(false);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Loading</p>{:then value}{#each row.labels as label}<input class="field" :data-label={label} ` + tc.attr + ` />{/each}{/await}{/each}</main></template>`
			sfc, err := parseSFC(source)
			if err != nil {
				t.Fatal(err)
			}
			if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
				t.Fatal("nested keyed-await binding was rejected by capability analysis")
			}
			program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
			if err != nil {
				t.Fatal(err)
			}
			code := PrintJS(program, PrintPretty)
			for _, want := range []string{"addEventListener", "$$set_" + tc.state, "removeEventListener"} {
				if !strings.Contains(code, want) {
					t.Fatalf("nested keyed-await binding lowering misses %q", want)
				}
			}
		})
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedRowsOwnStaticIfController(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#if field.visible}<button class="shown" @click={() => selected = field.id}>Shown</button>{:else}<p class="hidden">Hidden</p>{/if}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static direct if in nested keyed row was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"innerRows", "createComment('if')", "createComment('/if')", "addEventListener", "removeEventListener", "listeners", "destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed conditional controller misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedRowsOwnKeyController(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<button class:chosen={selected === field.id} :data-id={field.id} @click={() => selected = field.id}>{field.title}</button>{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("direct key in nested keyed row was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"createComment('key')", "createComment('/key')", "addEventListener", "removeEventListener", "classList.toggle", "listeners", "destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed key controller misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedRowsOwnConditionalKeyController(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}{#if field.visible}<button class="shown" @click={() => selected = field.id}>{field.title}</button>{:else}<input class="hidden" bind:value={selected} />{/if}{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("direct if in nested keyed key row was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"createComment('key')", "active: -1", "addEventListener", "removeEventListener", "destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed conditional controller misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedRowsOwnNestedEachKeyController(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let selected = $state(""); let draft = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}{#each field.tags as tag (tag)}<input :data-tag={tag} class:selected={selected === tag} style:color={selected ? "red" : "blue"} bind:value={draft} @click={() => selected = tag} />{/each}{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("direct each in nested keyed key row was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"createComment('key')", "createComment('each')", "createComment('/each')", "new Map()", "reconcileHydrationKeyedRowRanges", "addEventListener", "removeEventListener", "classList.toggle", "style.setProperty", "$$set_draft", "destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed each controller misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedRowsOwnDynamicFallbackController(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let draft = $state(""); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}{#each field.tags as tag (tag)}<p>{tag}</p>{:else}<input class="empty" :data-draft={draft} class:active={draft === "empty"} style:color={draft ? "red" : "blue"} bind:value={draft} @click={() => selected = "fallback"} />{/each}{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("dynamic fallback in nested keyed key row was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"_state = () =>", "_mount = (", "$$createHydrationRowController", "nodes: []", "updates: []", "listeners: []", "blocks.size", "addEventListener", "removeEventListener", "classList.toggle", "style.setProperty", "$$set_draft", "destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed each fallback controller misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedUnkeyedRowsOwnDynamicFallback(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]); let draft = $state(""); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}{#each field.tags as tag}<p>{tag}</p>{:else}<input class="empty" :data-draft={draft} class:active={draft === "empty"} style:color={draft ? "red" : "blue"} bind:value={draft} @click={() => selected = "fallback"} />{/each}{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("dynamic fallback in nested unkeyed key row was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{".length === 0", "listeners: []", "addEventListener", "removeEventListener", "classList.toggle", "style.setProperty", "$$set_draft", "createComment('each')", "destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested unkeyed each dynamic fallback misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedRowsOwnDirectKeyComponentController(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; let rows = $state.deep([]); let calls = $state(0); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child :id={field.id} :title={field.title} bind:value={selected} @on:close={() => calls++} />{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("direct child in nested keyed await key row was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$csr_Child", "components: []", ".components.push", ".set(", "$$onChange_value", "$$set_selected", "addEventListener", "removeEventListener", "instanceof CustomEvent", "$$set_calls", ".destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed child controller misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedChildRendersStaticSlots(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; let rows = $state.deep([]);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child :title={field.title}><span class="slot-body">Static body</span><svelte:fragment slot="actions"><button class="slot-action">Save</button></svelte:fragment></Child>{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static slots in a nested keyed await child were rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$csr_Child", "createDocumentFragment", "Static body", "slot-action", "components: []", ".components.push", ".destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed child static slot lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedChildUpdatesDynamicSlots(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child, Grandchild } from "./Child.vela"; let rows = $state.deep([]); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child :title={field.title}><span class="slot-body" :data-title={field.title}>{field.title}:{selected}</span><input class="slot-input" bind:value={selected} @click={() => selected = "clicked"} />{#if selected}<span class="slot-if">Selected {selected}</span>{:else}<span class="slot-if">Empty</span>{/if}{#each field.tags as tag (tag)}<button class="slot-tag" @click={() => selected = tag}>{tag}:{selected}</button><Grandchild :label={tag} />{#if selected === tag}<span class="slot-tag-state">active {tag}</span>{:else}<span class="slot-tag-state">idle {tag}</span>{/if}{#key selected}<span class="slot-tag-key">{tag}:{selected}</span>{/key}{:else}<p class="slot-empty">Empty tags</p>{/each}</Child>{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("dynamic text and attribute slot in a nested keyed await child were rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"slot-body", "slot-input", "slot-if", "slot-tag", "slot-tag-state", "slot-tag-key", "slot-key", "slot-empty", "slot-each", "slot-row", "$$reconcileHydrationKeyedRowRanges", "$$csr_Grandchild", "components: []", "blocks: new Map", "data-title", "$$set_selected", ".updates.push", "createTextNode", ".data =", "addEventListener", "removeEventListener"} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested keyed child dynamic slot lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedSlotRowChildOwnsEventsAndBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; import { Grandchild } from "./Grandchild.vela"; let rows = $state.deep([]); let selected = $state(""); let calls = $state(0);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child>{#each field.tags as tag (tag)}<Grandchild :label={tag + ":" + selected} bind:value={selected} @on:close={() => calls++} />{/each}</Child>{/key}{/each}<p>{calls}:{selected}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("event and $bindable child in a keyed slot row were rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$csr_Grandchild", "$$onChange_value", "$$set_selected", "$$set_calls", "instanceof CustomEvent", "addEventListener", "removeEventListener", "components: []", ".components.push", ".destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("keyed slot row child event/binding lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedSlotRowChildAdmitsStaticProjectedSlots(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; import { Grandchild } from "./Grandchild.vela"; let rows = $state.deep([]);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child>{#each field.tags as tag (tag)}<Grandchild :label={tag}><span class="projected">Static projection</span><svelte:fragment slot="actions"><button class="projected-action">Save</button></svelte:fragment></Grandchild>{/each}</Child>{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static projected slots in an inner keyed slot child were rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$csr_Grandchild", "createDocumentFragment", "Static projection", "projected-action", "$$insertHTML", "components: []", ".destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("static projected slot child lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedSlotRowChildAdmitsProjectedNestedComponent(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; import { Grandchild } from "./Grandchild.vela"; import { Nested } from "./Nested.vela"; let rows = $state.deep([]); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child>{#each field.tags as tag (tag)}<Grandchild><Nested :label={tag + ":" + selected} /></Grandchild>{/each}</Child>{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("projected nested component in an inner keyed slot child was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$csr_Grandchild", "$$csr_Nested", "components: []", ".components.push", ".set({ label:", ".destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("projected nested component lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedSlotRowChildAdmitsStructuralProjection(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; import { Grandchild } from "./Grandchild.vela"; import { Nested } from "./Nested.vela"; let rows = $state.deep([]); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child>{#each field.tags as tag (tag)}<Grandchild>{#if selected}<span class="projected-if">{tag}:{selected}</span>{:else}<span class="projected-if">Empty</span>{/if}{#key selected}<span class="projected-key">{tag}:{selected}</span>{/key}{#each field.labels as label (label)}<Nested :label={tag + ":" + label + ":" + selected} />{:else}<span class="projected-empty">No labels</span>{/each}</Grandchild>{/each}</Child>{/key}{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("structural projection in an inner keyed slot child was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"projected-if", "projected-key", "projected-empty", "createComment('slot-if')", "createComment('slot-key')", "createComment('slot-each')", "$$reconcileHydrationKeyedRowRanges", "$$csr_Nested", "components: []", "listeners: []", ".destroy"} {
		if !strings.Contains(code, want) {
			t.Fatalf("structural projected slot lowering misses %q:\n%s", want, code)
		}
	}
}

func TestSSRHydrationKeyedSlotRowProjectedStructuralCombinationCorpus(t *testing.T) {
	const prefix = `<script>import { Child } from "./Child.vela"; import { Grandchild } from "./Grandchild.vela"; import { Nested } from "./Nested.vela"; let rows = $state.deep([]); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child>{#each field.tags as tag (tag)}<Grandchild>`
	const suffix = `</Grandchild>{/each}</Child>{/key}{/each}{/await}{/each}</main></template>`
	cases := []struct {
		name string
		body string
	}{
		{
			name: "if-key-keyed-each-child",
			body: `{#if selected}{#key selected}{#each field.labels as label (label)}<Nested :label={tag + ":" + label + ":" + selected} />{:else}<span class="empty">Empty</span>{/each}{/key}{:else}<span class="off">Off</span>{/if}`,
		},
		{
			name: "key-if-keyed-each-child",
			body: `{#key selected}{#if selected}{#each field.labels as label (label)}<Nested :label={tag + ":" + label + ":" + selected} />{:else}<span class="empty">Empty</span>{/each}{:else}<span class="off">Off</span>{/if}{/key}`,
		},
		{
			name: "keyed-each-if-key-child",
			body: `{#each field.labels as label (label)}{#if selected}{#key selected}<Nested :label={tag + ":" + label + ":" + selected} />{/key}{:else}<button class="off" @click={() => selected = label}>{label}</button>{/if}{:else}<span class="empty">Empty</span>{/each}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sfc, err := parseSFC(prefix + tc.body + suffix)
			if err != nil {
				t.Fatal(err)
			}
			analysis := analyzeScriptAST(sfc.ScriptAST)
			if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
				t.Fatalf("combination was rejected:\n%s", tc.body)
			}
			program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
			if err != nil {
				t.Fatal(err)
			}
			code := PrintJS(program, PrintPretty)
			for _, want := range []string{"$$csr_Nested", "slot-if", "slot-key", "slot-each", "$$reconcileHydrationKeyedRowRanges", "components: []", "listeners: []", ".destroy"} {
				if !strings.Contains(code, want) {
					t.Fatalf("combination lowering misses %q:\n%s", want, code)
				}
			}
		})
	}
}

func TestBrowserSSRHydrationKeyedSlotRowProjectedStructuralCombinationCorpus(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<template><section class="slot-host"><slot></slot></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	grandchildSFC, err := parseSFC(`<script>export let label = "";</script><template><button class="grandchild" :data-label={label}><slot></slot></button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	grandchildAnalysis := analyzeScriptAST(grandchildSFC.ScriptAST)
	grandchildCSR, _ := buildProgramWithDOMAcquirePlan("Grandchild", grandchildSFC, grandchildSFC.TemplateAST.Nodes, grandchildAnalysis, "vela-grandchild", false, DOMAcquireCreate)
	nestedSFC, err := parseSFC(`<script>export let label = "";</script><template><b class="nested" :data-label={label}>{label}</b></template>`)
	if err != nil {
		t.Fatal(err)
	}
	nestedAnalysis := analyzeScriptAST(nestedSFC.ScriptAST)
	nestedCSR, _ := buildProgramWithDOMAcquirePlan("Nested", nestedSFC, nestedSFC.TemplateAST.Nodes, nestedAnalysis, "vela-nested", false, DOMAcquireCreate)

	const prefix = `<script>import { Child } from "./Child.vela"; import { Grandchild } from "./Grandchild.vela"; import { Nested } from "./Nested.vela"; let rows = $state.deep(window.__rows || []); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child>{#each field.tags as tag (tag)}<Grandchild :label={tag + ":" + selected}>`
	const suffix = `</Grandchild>{/each}</Child>{/key}{/each}{/await}{/each}</main></template>`
	cases := []struct {
		name            string
		body            string
		initialOffCount int
	}{
		{
			name:            "if-key-keyed-each",
			body:            `{#if selected}{#key selected}{#each field.labels as label (label)}<Nested :label={tag + ":" + label + ":" + selected} />{:else}<span class="empty">Empty</span>{/each}{/key}{:else}<span class="off">Off</span>{/if}`,
			initialOffCount: 2,
		},
		{
			name:            "key-if-keyed-each",
			body:            `{#key selected}{#if selected}{#each field.labels as label (label)}<Nested :label={tag + ":" + label + ":" + selected} />{:else}<span class="empty">Empty</span>{/each}{:else}<span class="off">Off</span>{/if}{/key}`,
			initialOffCount: 2,
		},
		{
			name:            "keyed-each-if-key",
			body:            `{#each field.labels as label (label)}{#if selected}{#key selected}<Nested :label={tag + ":" + label + ":" + selected} />{/key}{:else}<span class="off">Off</span>{/if}{:else}<span class="empty">Empty</span>{/each}`,
			initialOffCount: 4,
		},
		{
			name:            "if-keyed-each-key",
			body:            `{#if selected}{#each field.labels as label (label)}{#key selected}<Nested :label={tag + ":" + label + ":" + selected} />{/key}{:else}<span class="empty">Empty</span>{/each}{:else}<span class="off">Off</span>{/if}`,
			initialOffCount: 2,
		},
		{
			name:            "key-keyed-each-if",
			body:            `{#key selected}{#each field.labels as label (label)}{#if selected}<Nested :label={tag + ":" + label + ":" + selected} />{:else}<span class="off">Off</span>{/if}{:else}<span class="empty">Empty</span>{/each}{/key}`,
			initialOffCount: 4,
		},
		{
			name:            "keyed-each-key-if",
			body:            `{#each field.labels as label (label)}{#key selected}{#if selected}<Nested :label={tag + ":" + label + ":" + selected} />{:else}<span class="off">Off</span>{/if}{/key}{:else}<span class="empty">Empty</span>{/each}`,
			initialOffCount: 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sfc, err := parseSFC(prefix + tc.body + suffix)
			if err != nil {
				t.Fatal(err)
			}
			analysis := analyzeScriptAST(sfc.ScriptAST)
			plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
			if err != nil {
				t.Fatal(err)
			}
			client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
			if err != nil {
				t.Fatal(err)
			}
			entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
			if err != nil {
				t.Fatal(err)
			}
			main := sfc.TemplateAST.Nodes[0].(*ElementNode)
			each := main.Children[0].(*EachBlockNode)
			await := each.Children[0].(*AwaitBlockNode)
			markers := ssrHydrationMarkerIDs(plan.Nodes)
			eachID, ok := markers.eachID(each.Span)
			if !ok {
				t.Fatal("outer keyed each marker is missing")
			}
			awaitID, ok := markers.awaitID(await.Span)
			if !ok {
				t.Fatal("await marker is missing")
			}
			rowHTML := func(key string) string {
				rowScope := "vela-page--each-" + itoa(each.Span.Start) + "-" + key
				return `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><p vela-page="" class="pending">Pending</p><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
			}
			html := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(eachID) + `-->` + rowHTML("a") + rowHTML("b") + `<!--/v:vela-page:` + itoa(eachID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":["rows"],"incomplete":false}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.__rows=[{id:"a",request:window.promise,fields:[{id:"f",version:0,tags:["one"],labels:["x","y"]}]},{id:"b",request:window.promise,fields:[{id:"g",version:0,tags:["two"],labels:["x","y"]}]}];window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length</script></body>`
			dir := t.TempDir()
			for name, data := range map[string][]byte{
				"runtime.js":              embeddedRuntime,
				"Child.js":                []byte(PrintJS(childCSR, PrintPretty)),
				"Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)),
				"Child.hydrate.js":        []byte(PrintJS(childEntry, PrintPretty)),
				"Grandchild.js":           []byte(PrintJS(grandchildCSR, PrintPretty)),
				"Nested.js":               []byte(PrintJS(nestedCSR, PrintPretty)),
				"Page.hydrate_client.js":  []byte(PrintJS(client, PrintPretty)),
				"Page.hydrate.js":         []byte(PrintJS(entry, PrintPretty)),
				"index.html":              []byte(html),
			} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("pending adoption mutated DOM");await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelectorAll(".off").length===2);await p.evaluate(()=>window.hydrated.component.set({selected:"ready"}));await p.waitForFunction(()=>document.querySelectorAll(".nested").length===4&&Array.from(document.querySelectorAll(".nested")).every(x=>x.dataset.label.endsWith(":ready")));await p.evaluate(()=>{window.retained=Array.from(document.querySelectorAll(".grandchild"));window.hydrated.component.set({rows:[{id:"b",request:window.promise,fields:[{id:"g",version:0,tags:["two"],labels:["x","y"]}]},{id:"a",request:window.promise,fields:[{id:"f",version:0,tags:["one"],labels:[]}]}]})});await p.waitForFunction(()=>{const children=Array.from(document.querySelectorAll(".grandchild"));return children.length===2&&children.every(node=>window.retained.includes(node))&&document.querySelector(".empty")&&document.querySelectorAll(".nested").length===2&&Array.from(document.querySelectorAll(".nested")).every(node=>node.dataset.label.startsWith("two:"))});await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
			browserScript := strings.Replace(browser, `document.querySelectorAll(".off").length===2`, `document.querySelectorAll(".off").length===`+itoa(tc.initialOffCount), 1)
			command := exec.Command(node, "-e", browserScript, dir)
			command.Dir = "/projects/compiler"
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("projected structural browser corpus: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
			}
			if got := strings.TrimSpace(string(output)); got != "OK" {
				t.Fatalf("projected structural browser corpus output=%q", got)
			}
		})
	}
}

func TestSSRHydrationKeyedEachAwaitNestedKeyedChildRejectsUnownedDynamicSlotLifecycle(t *testing.T) {
	for _, source := range []string{
		`<script>import { Child } from "./Child.vela"; let rows = $state.deep([]);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child>{#each row.tags as tag (tag)}{#await row.request}<span>{tag}</span>{/await}{/each}</Child>{/key}{/each}{/await}{/each}</main></template>`,
	} {
		sfc, err := parseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		if CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
			t.Fatalf("dynamic slot lifecycle was accepted before it has an owner recipe:\n%s", source)
		}
	}
}

func TestBrowserSSRHydrationNestedKeyedAwaitDirectChildLifecycle(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let id = ""; export let title = ""; export let value = $bindable(""); onDestroy(() => { window.__nestedDirectChildDestroys = (window.__nestedDirectChildDestroys || 0) + 1; });</script><template><p class="child" :data-id={id} :data-title={title}>{title}<slot><span class="child-fallback">fallback</span></slot><slot name="actions"></slot><button class="child-set" @click={() => value = "bound"}>Bind</button></p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; let rows = $state.deep(window.__rows || []); let selected = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child :id={field.id} :title={field.title} bind:value={selected}><span class="slot-body" :data-title={field.title}>{field.title}:{selected}</span><input class="slot-input" bind:value={selected} @click={() => selected = "clicked"} />{#if selected}<span class="slot-if">Selected {selected}</span>{:else}<span class="slot-if">Empty</span>{/if}{#each field.tags as tag (tag)}<button class="slot-tag" @click={() => selected = tag}>{tag}:{selected}</button>{#if selected === tag}<span class="slot-tag-state">active {tag}</span>{:else}<span class="slot-tag-state">idle {tag}</span>{/if}{#key selected}<span class="slot-tag-key">{tag}:{selected}</span>{/key}{:else}<p class="slot-empty">Empty tags</p>{/each}<svelte:fragment slot="actions"><button class="slot-action">Save</button></svelte:fragment></Child>{/key}{/each}<button class="reorder-tags" @click={() => rows[0].fields[0].tags.reverse()}>Reorder</button><button class="update" @click={() => rows[0].fields[0].title = "updated"}>Update</button><button class="choose" @click={() => selected = "chosen"}>Choose</button><button class="replace" @click={() => rows[0].fields[0].version++}>Replace</button><button class="remove" @click={() => rows = []}>Remove</button><p class="selected">{selected}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("outer each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("nested await marker is missing")
	}
	rowScope := "vela-parent--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-parent:` + itoa(awaitID) + `--><p vela-parent="" class="pending">Loading</p><!--/v:vela-parent:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	html := `<!doctype html><body><!--e:vela-parent:0--><main vela-parent=""><!--v:vela-parent:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-parent:` + itoa(eachID) + `--></main><!--/e:vela-parent:0--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{},"state":{},"deep":["rows"],"incomplete":false}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.__rows=[{id:"a",request:window.promise,fields:[{id:"f",title:"initial",version:0,tags:["one","two"]}]}];window.hydrated=hydrateParent(document.body)</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Child.js": []byte(PrintJS(childCSR, PrintPretty)), "Child.hydrate_client.js": []byte(PrintJS(childClient, PrintPretty)), "Child.hydrate.js": []byte(PrintJS(childEntry, PrintPretty)), "Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)), "Parent.hydrate.js": []byte(PrintJS(parentEntry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const script = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelector(".child")?.dataset.title==="initial"&&document.querySelector(".slot-body")?.textContent==="initial:"&&document.querySelector(".slot-body")?.dataset.title==="initial"&&document.querySelector(".slot-input")?.value===""&&document.querySelector(".slot-if")?.textContent==="Empty"&&document.querySelector(".slot-action")?.textContent==="Save"&&document.querySelectorAll(".slot-tag").length===2);await p.evaluate(()=>{window.firstTag=document.querySelector(".slot-tag");document.querySelector(".reorder-tags").click()});await p.waitForFunction(()=>document.querySelectorAll(".slot-tag")[1]===window.firstTag&&document.querySelector(".slot-tag")?.textContent==="two:");await p.evaluate(()=>{window.emptySlotIf=document.querySelector(".slot-if");window.firstTag.click()});await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="one"&&document.querySelector(".slot-body")?.textContent==="initial:one");await p.evaluate(()=>document.querySelector(".child-set").click());await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="bound"&&document.querySelector(".slot-body")?.textContent==="initial:bound"&&document.querySelector(".slot-input")?.value==="bound"&&document.querySelector(".slot-if")?.textContent==="Selected bound"&&document.querySelector(".slot-if")!==window.emptySlotIf);await p.evaluate(()=>document.querySelector(".slot-input").click());await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="clicked"&&document.querySelector(".slot-body")?.textContent==="initial:clicked"&&document.querySelector(".slot-input")?.value==="clicked"&&document.querySelector(".slot-if")?.textContent==="Selected clicked");await p.evaluate(()=>{const input=document.querySelector(".slot-input");input.value="typed";input.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="typed"&&document.querySelector(".slot-body")?.textContent==="initial:typed"&&document.querySelector(".slot-if")?.textContent==="Selected typed");await p.evaluate(()=>{window.first=document.querySelector(".child");window.firstSlot=document.querySelector(".slot-body");document.querySelector(".update").click()});await p.waitForFunction(()=>document.querySelector(".child")?.dataset.title==="updated"&&document.querySelector(".slot-body")?.textContent==="updated:typed"&&document.querySelector(".slot-body")?.dataset.title==="updated");if(!(await p.evaluate(()=>window.first===document.querySelector(".child")&&window.firstSlot===document.querySelector(".slot-body")&&window.firstTag===document.querySelectorAll(".slot-tag")[1])))throw new Error("child or keyed dynamic slot was recreated instead of updated");await p.evaluate(()=>document.querySelector(".choose").click());await p.waitForFunction(()=>document.querySelector(".slot-body")?.textContent==="updated:chosen"&&document.querySelector(".slot-input")?.value==="chosen"&&document.querySelector(".slot-if")?.textContent==="Selected chosen");await p.evaluate(()=>{document.querySelector(".replace").click()});await p.waitForFunction(()=>document.querySelector(".child")?.dataset.title==="updated"&&document.querySelector(".child")!==window.first&&document.querySelector(".slot-body")!==window.firstSlot&&document.querySelector(".slot-body")?.textContent==="updated:chosen"&&document.querySelector(".slot-if")?.textContent==="Selected chosen");await p.evaluate(()=>{window.second=document.querySelector(".child");document.querySelector(".remove").click()});await p.waitForFunction(()=>!document.querySelector(".child"));const beforeDestroy=await p.evaluate(()=>({first:!window.first.isConnected,second:!window.second.isConnected,destroys:window.__nestedDirectChildDestroys}));if(!beforeDestroy.first||!beforeDestroy.second||beforeDestroy.destroys!==2)throw new Error(JSON.stringify(beforeDestroy));await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", script, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed await child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed await child browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedSlotRowChildLifecycle(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<template><section class="slot-host"><slot></slot></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)
	grandchildSFC, err := parseSFC(`<script>export let label = ""; export let value = $bindable(""); import { onDestroy } from "vela"; onDestroy(() => { window.__keyedSlotGrandchildDestroys = (window.__keyedSlotGrandchildDestroys || 0) + 1; });</script><template><button class="grandchild" :data-label={label} :data-value={value} @click={() => { value = "child"; $$dispatch("close") }}>{label}<slot></slot><slot name="actions"></slot></button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	grandchildAnalysis := analyzeScriptAST(grandchildSFC.ScriptAST)
	grandchildPlan, err := BuildSSRRenderPlanWithAnalysis("Grandchild", grandchildSFC, grandchildAnalysis, "vela-grandchild")
	if err != nil {
		t.Fatal(err)
	}
	grandchildClient, err := BuildSSRHydrationClientProgram("Grandchild", grandchildSFC, grandchildAnalysis, "vela-grandchild")
	if err != nil {
		t.Fatal(err)
	}
	grandchildEntry, err := BuildSSRHydrateEntryWithClient(grandchildPlan, "./Grandchild.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	grandchildCSR, _ := buildProgramWithDOMAcquirePlan("Grandchild", grandchildSFC, grandchildSFC.TemplateAST.Nodes, grandchildAnalysis, "vela-grandchild", false, DOMAcquireCreate)
	nestedSFC, err := parseSFC(`<script>export let label = ""; import { onDestroy } from "vela"; onDestroy(() => { window.__keyedSlotNestedDestroys = (window.__keyedSlotNestedDestroys || 0) + 1; });</script><template><b class="nested" :data-label={label}>{label}</b></template>`)
	if err != nil {
		t.Fatal(err)
	}
	nestedAnalysis := analyzeScriptAST(nestedSFC.ScriptAST)
	nestedCSR, _ := buildProgramWithDOMAcquirePlan("Nested", nestedSFC, nestedSFC.TemplateAST.Nodes, nestedAnalysis, "vela-nested", false, DOMAcquireCreate)
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; import { Grandchild } from "./Grandchild.vela"; import { Nested } from "./Nested.vela"; let rows = $state.deep(window.__rows || []); let selected = $state(""); let calls = $state(0);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<Child>{#each field.tags as tag (tag)}<Grandchild :label={tag + ":" + selected} bind:value={selected} @on:close={() => calls++}><span class="projected" :data-selected={selected}>{tag}:{selected}</span>{#if selected}<span class="projected-if">{tag}:{selected}</span>{:else}<span class="projected-if">Empty</span>{/if}{#key selected}<span class="projected-key">{tag}:{selected}</span>{/key}{#each field.labels as label (label)}<Nested :label={tag + ":" + label + ":" + selected} />{:else}<span class="projected-empty">No labels</span>{/each}<svelte:fragment slot="actions">{#if selected}{#key selected}{#each field.labels as label (label)}<Nested :label={"action:" + tag + ":" + label + ":" + selected} />{:else}<span class="action-empty">No labels</span>{/each}{/key}{:else}<span class="action-off">Off</span>{/if}</svelte:fragment></Grandchild>{/each}</Child>{/key}{/each}<button class="reorder" @click={() => rows[0].fields[0].tags.reverse()}>Reorder</button><button class="choose" @click={() => selected = "chosen"}>Choose</button><button class="clear-labels" @click={() => rows[0].fields[0].labels = []}>Clear labels</button><button class="clear" @click={() => rows[0].fields[0].tags = []}>Clear</button><button class="remove" @click={() => rows = []}>Remove</button><p class="calls">{calls}:{selected}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(parentPlan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("outer each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("nested await marker is missing")
	}
	rowScope := "vela-parent--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-parent:` + itoa(awaitID) + `--><p vela-parent="" class="pending">Loading</p><!--/v:vela-parent:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	html := `<!doctype html><body><!--e:vela-parent:0--><main vela-parent=""><!--v:vela-parent:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-parent:` + itoa(eachID) + `--></main><!--/e:vela-parent:0--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{},"state":{},"deep":["rows"],"incomplete":false}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.__rows=[{id:"a",request:window.promise,fields:[{id:"f",version:0,tags:["one","two"],labels:["x","y"]}]}];window.hydrated=hydrateParent(document.body)</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":                   embeddedRuntime,
		"Child.js":                     []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":      []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":             []byte(PrintJS(childEntry, PrintPretty)),
		"Grandchild.js":                []byte(PrintJS(grandchildCSR, PrintPretty)),
		"Grandchild.hydrate_client.js": []byte(PrintJS(grandchildClient, PrintPretty)),
		"Grandchild.hydrate.js":        []byte(PrintJS(grandchildEntry, PrintPretty)),
		"Nested.js":                    []byte(PrintJS(nestedCSR, PrintPretty)),
		"Parent.hydrate_client.js":     []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":            []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":                   []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const script = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelectorAll(".grandchild").length===2&&document.querySelectorAll(".projected").length===2&&document.querySelectorAll(".action-off").length===2&&document.querySelectorAll(".nested").length===4);await p.evaluate(()=>{window.one=document.querySelector('[data-label="one:"]');window.projection=window.one.querySelector(".projected");window.keyNode=window.one.querySelector(".projected-key");window.nested=window.one.querySelector('[data-label="one:x:"]')});await p.waitForFunction(()=>window.projection?.textContent==="one:"&&window.projection.dataset.selected===""&&window.one.querySelector(".projected-if")?.textContent==="Empty"&&window.keyNode?.textContent==="one:"&&window.nested);await p.evaluate(()=>document.querySelector(".reorder").click());await p.waitForFunction(()=>document.querySelectorAll(".grandchild")[1]===window.one&&document.querySelectorAll(".grandchild")[0]?.dataset.label==="two:"&&document.querySelectorAll(".grandchild")[1].querySelector(".projected")===window.projection&&document.querySelectorAll(".grandchild")[1].querySelector('[data-label="one:x:"]')===window.nested);await p.evaluate(()=>window.one.click());await p.waitForFunction(()=>window.one===document.querySelector('[data-label="one:child"]')&&window.one.dataset.value==="child"&&window.projection.textContent==="one:child"&&window.projection.dataset.selected==="child"&&window.one.querySelector(".projected-if")?.textContent==="one:child"&&window.one.querySelector(".projected-key")!==window.keyNode&&window.one.querySelector(".projected-key")?.textContent==="one:child"&&window.nested.dataset.label==="one:x:child"&&document.querySelectorAll(".nested").length===8&&document.querySelector('[data-label="action:one:x:child"]')&&document.querySelector(".calls")?.textContent==="1:child");await p.evaluate(()=>{window.keyNode=window.one.querySelector(".projected-key");window.actionNested=window.one.querySelector('[data-label="action:one:x:child"]');document.querySelector(".choose").click()});await p.waitForFunction(()=>window.one===document.querySelector('[data-label="one:chosen"]')&&window.one.dataset.value==="chosen"&&window.projection.textContent==="one:chosen"&&window.projection.dataset.selected==="chosen"&&window.one.querySelector(".projected-if")?.textContent==="one:chosen"&&window.one.querySelector(".projected-key")!==window.keyNode&&window.one.querySelector(".projected-key")?.textContent==="one:chosen"&&window.nested.dataset.label==="one:x:chosen"&&window.actionNested!==window.one.querySelector('[data-label="action:one:x:chosen"]')&&document.querySelector(".calls")?.textContent==="1:chosen");await p.evaluate(()=>{window.oldNested=window.nested;window.oldActionNested=window.one.querySelector('[data-label="action:one:x:chosen"]');document.querySelector(".clear-labels").click()});await p.waitForFunction(()=>document.querySelectorAll(".grandchild").length===2&&!document.querySelector(".nested")&&document.querySelectorAll(".projected-empty").length===2&&document.querySelectorAll(".action-empty").length===2&&!window.oldNested.isConnected&&!window.oldActionNested.isConnected);await p.evaluate(()=>{window.old=window.one;document.querySelector(".clear").click()});await p.waitForFunction(()=>!document.querySelector(".grandchild")&&!document.querySelector(".projected")&&!document.querySelector(".nested")&&!document.querySelector(".projected-if")&&!document.querySelector(".projected-key")&&!document.querySelector(".action-empty"));await p.evaluate(async()=>{window.old.click();await new Promise(resolve=>setTimeout(resolve,0));window.stale=document.querySelector(".calls").textContent});if((await p.evaluate(()=>window.stale))!=="1:chosen")throw new Error("detached child retained event or binding callback: "+await p.evaluate(()=>window.stale));const destroys=await p.evaluate(()=>({grandchild:window.__keyedSlotGrandchildDestroys,nested:window.__keyedSlotNestedDestroys}));if(destroys.grandchild!==2||destroys.nested!==12)throw new Error("unexpected nested teardown calls: "+JSON.stringify(destroys));await p.evaluate(()=>document.querySelector(".remove").click());await p.waitForFunction(()=>!document.querySelector(".reorder"));await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left parent root attached");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", script, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed slot row child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed slot row child browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitNestedUnkeyedRowsDynamicFallback(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows || []); let selected = $state(""); let draft = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}{#each field.tags as tag}<input class="tag" :data-tag={tag} bind:value={draft} @click={() => selected = tag} />{:else}<input class="empty" :data-draft={draft} class:active={draft === "empty"} style:color={draft ? "red" : "blue"} bind:value={draft} @click={() => selected = "fallback"} />{/each}{/key}{/each}<button class="clear" @click={() => { rows[0].fields[0].tags = []; selected = ""; draft = "empty" }}>Clear</button><p class="selected">{selected}</p><p class="draft">{draft}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	rowScope := "vela-page--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><p vela-page="" class="pending">Loading</p><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	serverHTML := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-page:` + itoa(eachID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":["rows"],"incomplete":false}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.__rows=[{id:"a",request:window.promise,fields:[{id:"a",version:0,tags:["one"]}]}];window.hydrated=hydratePage(document.body)</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelector(".tag"));await p.evaluate(()=>{window.oldTag=document.querySelector(".tag");document.querySelector(".clear").click()});await p.waitForFunction(()=>{const x=document.querySelector(".empty");return !document.querySelector(".tag")&&x?.value==="empty"&&x.dataset.draft==="empty"&&x.classList.contains("active")&&x.style.color==="red"});await p.evaluate(async()=>{window.oldTag.click();window.oldTag.value="stale";window.oldTag.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.stale={selected:document.querySelector("p.selected").textContent,draft:document.querySelector(".draft").textContent};const x=document.querySelector(".empty");x.click();x.value="fresh";x.dispatchEvent(new Event("input",{bubbles:true}))});const stale=await p.evaluate(()=>window.stale);if(stale.selected!==""||stale.draft!=="empty")throw new Error("removed unkeyed row retained listener or binding: "+JSON.stringify(stale));await p.waitForFunction(()=>{const x=document.querySelector(".empty");return document.querySelector("p.selected")?.textContent==="fallback"&&document.querySelector(".draft")?.textContent==="fresh"&&x.dataset.draft==="fresh"&&!x.classList.contains("active")&&x.style.color==="red"});await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested unkeyed fallback browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested unkeyed fallback browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitNestedKeyedRowsReplaceInnerKeyRange(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows || []); let selected = $state(""); let draft = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}<input class="field" :data-id={field.id} class:chosen={selected === field.id} style:color={selected ? "red" : "blue"} bind:value={draft} @click={() => selected = field.id} />{/key}{/each}<button class="bump" @click={() => { rows[0].fields[0].version++; selected = ""; draft = "reset" }}>Bump</button><p class="selected">{selected}</p><p class="draft">{draft}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	rowScope := "vela-page--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><p vela-page="" class="pending">Loading</p><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	serverHTML := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-page:` + itoa(eachID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":["rows"],"incomplete":false}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.__rows=[{id:"a",request:window.promise,fields:[{id:"a",title:"A",version:0}]}];window.hydrated=hydratePage(document.body)</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelector(".field"));const initial=await p.evaluate(()=>{const x=document.querySelector(".field");return {id:x.dataset.id,value:x.value,color:x.style.color,chosen:x.classList.contains("chosen")}});if(initial.id!=="a"||initial.value!==""||initial.color!=="blue"||initial.chosen)throw new Error("bad initial inner-key state: "+JSON.stringify(initial));await p.evaluate(()=>{window.oldField=document.querySelector(".field");window.oldField.click();window.oldField.value="typed";window.oldField.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="a"&&document.querySelector(".draft")?.textContent==="typed"&&document.querySelector(".field")?.classList.contains("chosen")&&document.querySelector(".field")?.style.color==="red");await p.evaluate(()=>document.querySelector(".bump").click());await p.waitForFunction(()=>{const x=document.querySelector(".field");return x!==window.oldField&&x?.value==="reset"&&!x.classList.contains("chosen")&&x.style.color==="blue"});await p.evaluate(async()=>{window.oldField.click();window.oldField.value="stale";window.oldField.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.afterStale={selected:document.querySelector("p.selected").textContent,draft:document.querySelector(".draft").textContent};const x=document.querySelector(".field");x.click();x.value="fresh";x.dispatchEvent(new Event("input",{bubbles:true}))});const stale=await p.evaluate(()=>window.afterStale);if(stale.selected!==""||stale.draft!=="reset")throw new Error("old inner-key listener or binding survived replacement: "+JSON.stringify(stale));await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="a"&&document.querySelector(".draft")?.textContent==="fresh");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed key browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed key browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitNestedKeyedRowsSwitchKeyConditional(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows || []); let selected = $state(""); let draft = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.fields as field (field.id)}{#key field.version}{#each field.tags as tag (tag)}<input class="tag" :data-tag={tag} class:selected={selected === tag} style:color={selected ? "red" : "blue"} bind:value={draft} @click={() => selected = tag} />{:else}<input class="empty-tags" :data-draft={draft} class:empty-active={draft === "empty"} style:color={draft ? "red" : "blue"} bind:value={draft} @click={() => selected = "fallback"} />{/each}{#if field.visible}<button class="shown" @click={() => selected = "shown"}>{field.title}</button>{:else}<button class="hidden" @click={() => selected = "hidden"}>{field.title}</button>{/if}{/key}{/each}<button class="replace-tags" @click={() => rows[0].fields[0].tags = ["two", "one", "next"]}>Tags</button><button class="remove-tags" @click={() => { rows[0].fields[0].tags = ["two"]; selected = ""; draft = "after" }}>Remove</button><button class="clear-tags" @click={() => { rows[0].fields[0].tags = []; selected = ""; draft = "empty" }}>Clear</button><button class="toggle" @click={() => { rows[0].fields[0].visible = !rows[0].fields[0].visible; selected = "" }}>Toggle</button><button class="bump" @click={() => { rows[0].fields[0].version++; selected = "" }}>Bump</button><p class="selected">{selected}</p><p class="draft">{draft}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	rowScope := "vela-page--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><p vela-page="" class="pending">Loading</p><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	serverHTML := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-page:` + itoa(eachID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":["rows"],"incomplete":false}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.__rows=[{id:"a",request:window.promise,fields:[{id:"a",title:"A",version:0,visible:true,tags:["one","two"]}]}];window.hydrated=hydratePage(document.body)</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelectorAll(".tag").length===2&&document.querySelector(".shown")?.textContent==="A");await p.evaluate(()=>{window.tagOne=document.querySelector('[data-tag="one"]');window.tagTwo=document.querySelector('[data-tag="two"]');window.tagOne.click()});await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="one");await p.evaluate(()=>{window.tagOne.value="typed";window.tagOne.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>document.querySelector(".draft")?.textContent==="typed");await p.evaluate(()=>document.querySelector(".replace-tags").click());await p.waitForFunction(()=>Array.from(document.querySelectorAll(".tag")).map(x=>x.dataset.tag).join("|")==="two|one|next");if(!(await p.evaluate(()=>document.querySelectorAll(".tag")[0]===window.tagTwo&&document.querySelectorAll(".tag")[1]===window.tagOne)))throw new Error("keyed nested rows were recreated during reorder");await p.evaluate(()=>document.querySelector(".remove-tags").click());await p.waitForFunction(()=>document.querySelectorAll(".tag").length===1&&document.querySelector('.tag')===window.tagTwo&&document.querySelector(".draft")?.textContent==="after");await p.evaluate(async()=>{window.tagOne.click();window.tagOne.value="stale";window.tagOne.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.afterStale={selected:document.querySelector("p.selected").textContent,draft:document.querySelector(".draft").textContent};document.querySelector(".tag").click()});const stale=await p.evaluate(()=>window.afterStale);if(stale.selected!==""||stale.draft!=="after")throw new Error("removed keyed row retained listener or binding: "+JSON.stringify(stale));await p.waitForFunction(()=>document.querySelector("p.selected")?.textContent==="two");await p.evaluate(()=>document.querySelector(".clear-tags").click());await p.waitForFunction(()=>{const x=document.querySelector(".empty-tags");return document.querySelectorAll(".tag").length===0&&x?.value==="empty"&&x.dataset.draft==="empty"&&x.classList.contains("empty-active")&&x.style.color==="red"&&document.querySelector(".draft")?.textContent==="empty"});await p.evaluate(async()=>{window.tagTwo.click();window.tagTwo.value="stale-empty";window.tagTwo.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.afterEmptyStale={selected:document.querySelector("p.selected").textContent,draft:document.querySelector(".draft").textContent};const x=document.querySelector(".empty-tags");x.click();x.value="fallback-input";x.dispatchEvent(new Event("input",{bubbles:true}))});const emptyStale=await p.evaluate(()=>window.afterEmptyStale);if(emptyStale.selected!==""||emptyStale.draft!=="empty")throw new Error("fallback retained a removed keyed row listener or binding: "+JSON.stringify(emptyStale));await p.waitForFunction(()=>{const x=document.querySelector(".empty-tags");return document.querySelector("p.selected")?.textContent==="fallback"&&document.querySelector(".draft")?.textContent==="fallback-input"&&x?.dataset.draft==="fallback-input"&&!x.classList.contains("empty-active")&&x.style.color==="red"});await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed conditional browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed conditional browser output=%q", got)
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitNestedEachValueBinding(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let rows = []; export let draft = "";</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.labels as label}<input class="nested-input" :data-label={label} bind:value={draft} />{/each}<p class="output">{draft}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	rowScope := "vela-page--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><p vela-page="" class="pending">Loading</p><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	serverHTML := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-page:` + itoa(eachID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","labels":["one","two"]}],"Draft":""},"state":{},"incomplete":false}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{rows:[{id:"a",labels:["one","two"],request:window.promise}]});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelectorAll(".nested-input").length===2);await p.evaluate(()=>{window.old=document.querySelectorAll(".nested-input")[1];window.old.value="typed";window.old.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>document.querySelector(".output")?.textContent==="typed"&&Array.from(document.querySelectorAll(".nested-input")).every(x=>x.value==="typed"));await p.evaluate(async()=>{window.old.value="stale";window.old.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.stale=document.querySelector(".output").textContent});if((await p.evaluate(()=>window.stale))!=="typed")throw new Error("stale nested binding listener remained attached");await p.evaluate(()=>window.hydrated.component.set({draft:"server"}));await p.waitForFunction(()=>document.querySelector(".output")?.textContent==="server"&&Array.from(document.querySelectorAll(".nested-input")).every(x=>x.value==="server"));await p.evaluate(()=>window.hydrated.component.destroy());const result=await p.evaluate(()=>({initial:window.initial,main:document.querySelector("main")}));if(result.initial!==0||result.main)throw new Error(JSON.stringify(result));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed-await binding browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed-await binding browser output=%q", got)
	}
}

func TestSSRHydrationKeyedEachAwaitNestedEachSiblingElements(t *testing.T) {
	sfc, err := parseSFC(`<script>export let rows = []; let draft = $state("");</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Loading</p>{:then value}{#each row.labels as label}<input bind:value={draft} /><button @click={() => draft = label}>{label}</button>{/each}<p>{draft}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("nested keyed-await sibling row was rejected by capability analysis")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	if got := strings.Count(code, "document.createElement"); got < 4 {
		t.Fatalf("sibling row lowering created %d elements, want pending/input/button/output:\n%s", got, code)
	}
	for _, want := range []string{"$$set_draft", "addEventListener", "removeEventListener"} {
		if !strings.Contains(code, want) {
			t.Fatalf("sibling row lowering misses %q:\n%s", want, code)
		}
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitNestedEachSiblingElements(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let rows = []; export let draft = "";</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.labels as label (label)}<input class="nested-input" :data-label={label} bind:value={draft} /><button class="nested-button" @click={() => draft = label}>{label}</button>{/each}<p class="output">{draft}</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	rowScope := "vela-page--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><p vela-page="" class="pending">Loading</p><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	serverHTML := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-page:` + itoa(eachID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","labels":["one","two"]}],"Draft":""},"state":{},"incomplete":false}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{rows:[{id:"a",labels:["one","two"],request:window.promise}]});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelectorAll(".nested-input").length===2&&document.querySelectorAll(".nested-button").length===2);await p.evaluate(()=>{window.one=document.querySelector("[data-label=one]");window.two=document.querySelector("[data-label=two]");window.hydrated.component.set({rows:[{id:"a",labels:["two","one","three"],request:window.promise}]})});await p.waitForFunction(()=>Array.from(document.querySelectorAll(".nested-input")).map(x=>x.dataset.label).join("|")==="two|one|three");if(!(await p.evaluate(()=>document.querySelector("[data-label=one]")===window.one&&document.querySelector("[data-label=two]")===window.two)))throw new Error("nested keyed rows were replaced instead of retained");await p.evaluate(()=>document.querySelectorAll(".nested-button")[0].click());await p.waitForFunction(()=>document.querySelector(".output")?.textContent==="two"&&Array.from(document.querySelectorAll(".nested-input")).every(x=>x.value==="two"));await p.evaluate(()=>{window.old=document.querySelectorAll(".nested-input")[0];window.old.value="typed";window.old.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>document.querySelector(".output")?.textContent==="typed"&&Array.from(document.querySelectorAll(".nested-input")).every(x=>x.value==="typed"));await p.evaluate(async()=>{window.old.value="stale";window.old.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.stale=document.querySelector(".output").textContent});if((await p.evaluate(()=>window.stale))!=="stale")throw new Error("retained nested binding listener stopped receiving input");await p.evaluate(()=>window.hydrated.component.set({draft:"server"}));await p.waitForFunction(()=>document.querySelector(".output")?.textContent==="server"&&Array.from(document.querySelectorAll(".nested-input")).every(x=>x.value==="server"));await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",labels:["two","three"],request:window.promise}]}));await p.waitForFunction(()=>document.querySelectorAll(".nested-input").length===2&&!document.querySelector("[data-label=one]"));await p.evaluate(async()=>{window.one.value="removed";window.one.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.removedValue=document.querySelector(".output").textContent});if((await p.evaluate(()=>window.removedValue))!=="server")throw new Error("removed nested keyed row listener remained attached");await p.evaluate(()=>window.hydrated.component.destroy());const result=await p.evaluate(()=>({initial:window.initial,main:document.querySelector("main")}));if(result.initial!==0||result.main)throw new Error(JSON.stringify(result));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested keyed-await sibling browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested keyed-await sibling browser output=%q", got)
	}
}

func TestSSRHydrationKeyedEachAwaitNestedEachDeepItemBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state.deep([]);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Loading</p>{:then value}{#each row.fields as field}<input bind:value={field.title} /><p>{field.title}</p>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("deep item binding was rejected by capability analysis")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$nested_item.title = $event.target.value", "addEventListener", "removeEventListener", "$$set_rows_notify"} {
		if !strings.Contains(code, want) {
			t.Fatalf("deep item binding lowering misses %q:\n%s", want, code)
		}
	}
}

func TestBrowserSSRHydrationKeyedEachAwaitNestedEachDeepItemBinding(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let rows = $state.deep(window.__rows || []);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p class="pending">Loading</p>{:then value}{#each row.fields as field}<input class="field" :data-id={field.id} bind:value={field.title} /><p class="value">{field.title}</p>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	each := main.Children[0].(*EachBlockNode)
	await := each.Children[0].(*AwaitBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	rowScope := "vela-page--each-" + itoa(each.Span.Start) + "-a"
	rowHTML := `<!--r:` + rowScope + `:` + itoa(eachID) + `--><!--v:vela-page:` + itoa(awaitID) + `--><p vela-page="" class="pending">Loading</p><!--/v:vela-page:` + itoa(awaitID) + `--><!--/r:` + rowScope + `:` + itoa(eachID) + `-->`
	serverHTML := `<!doctype html><body><!--e:vela-page:0--><main vela-page=""><!--v:vela-page:` + itoa(eachID) + `-->` + rowHTML + `<!--/v:vela-page:` + itoa(eachID) + `--></main><!--/e:vela-page:0--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"incomplete":false}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.promise=new Promise(resolve=>{window.resolve=resolve});window.__rows=[{id:"a",request:window.promise,fields:[{id:"one",title:"One"},{id:"two",title:"Two"}]}];window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body);window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(serverHTML)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);await p.evaluate(()=>window.resolve("done"));await p.waitForFunction(()=>document.querySelectorAll(".field").length===2);await p.evaluate(()=>{window.old=document.querySelector("[data-id=two]");window.old.value="Changed";window.old.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>Array.from(document.querySelectorAll(".value")).map(x=>x.textContent).join("|")==="One|Changed");await p.evaluate(async()=>{window.old.value="stale";window.old.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.stale=Array.from(document.querySelectorAll(".value")).map(x=>x.textContent).join("|")});if((await p.evaluate(()=>window.stale))!=="One|Changed")throw new Error("stale deep item binding listener remained attached");await p.evaluate(()=>window.hydrated.component.destroy());const result=await p.evaluate(()=>({initial:window.initial,main:document.querySelector("main")}));if(result.initial!==0||result.main)throw new Error(JSON.stringify(result));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("deep item binding browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("deep item binding browser output=%q", got)
	}
}

func TestSSRHydrationKeyedEachAwaitNestedEachShallowItemBindingReplacesList(t *testing.T) {
	sfc, err := parseSFC(`<script>let rows = $state([]);</script><template><main>{#each rows as row (row.id)}{#await row.request}<p>Loading</p>{:then value}{#each row.fields as field}<input bind:value={field.title} />{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, want := range []string{"$$nested_item.title = $event.target.value", "$$set_rows(rows.slice())"} {
		if !strings.Contains(code, want) {
			t.Fatalf("shallow item binding lowering misses %q:\n%s", want, code)
		}
	}
}
