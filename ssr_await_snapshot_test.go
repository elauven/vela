package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedSSRRendererSelectsAwaitSnapshot(t *testing.T) {
	sfc, err := parseSFC(`<script>export let draft = "server"; export let enabled = false;</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<p class="settled" :title={value}>Settled</p><input class="draft" bind:value={draft} /><input class="enabled" type="checkbox" bind:checked={enabled} /><button @click={clicked}>Save</button>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGeneratedSSRGo("page.go", renderer); err != nil {
		t.Fatalf("ValidateGeneratedSSRGo: %v\n%s", err, renderer)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	awaitID, ok := ssrHydrationMarkerIDs(plan.Nodes).awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}

	root := t.TempDir()
	components := filepath.Join(root, "components")
	if err := os.MkdirAll(components, 0o755); err != nil {
		t.Fatal(err)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module example.com/awaitsnapshot\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(components, "page.go"), renderer, 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
  "os"
  "example.com/awaitsnapshot/components"
)
func main() {
  err := components.RenderPageWithOptionsAt(os.Stdout, components.PageProps{Draft: "server", Enabled: false}, components.PageSlots{}, components.PageRenderOptions{
    Await: map[int]components.PageAwaitSnapshot{` + itoa(awaitID) + `: {State: "then", Value: "server"}},
  }, "vela-page-instance")
  if err != nil { panic(err) }
}

`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{
		`<p class="settled" title="server">Settled</p>`,
		`<input class="draft" value="server">`,
		`<input class="enabled" type="checkbox">`,
		`<button data-vela-event="vela-page-e0">Save</button>`,
		`"await":{"` + itoa(awaitID) + `":{"state":"then","value":"server"}}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("SSR settled snapshot misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Pending") || strings.Contains(got, "Failed") {
		t.Fatalf("SSR settled snapshot rendered another await branch:\n%s", got)
	}
}

func TestGeneratedSSRRendererSelectsAwaitSnapshotStaticKeyedEach(t *testing.T) {
	sfc, err := parseSFC(`<script>export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row)}<p class="settled-row">Row</p>{:else}<p class="settled-empty">Empty</p>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGeneratedSSRGo("page.go", renderer); err != nil {
		t.Fatalf("ValidateGeneratedSSRGo: %v\n%s", err, renderer)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	each := await.ThenChildren[0].(*EachBlockNode)
	markers := ssrHydrationMarkerIDs(plan.Nodes)
	awaitID, ok := markers.awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}
	eachID, ok := markers.eachID(each.Span)
	if !ok {
		t.Fatal("keyed each marker is missing")
	}
	for _, want := range []string{
		`case "then":`,
		`for _, ssrEachItem0 := range ssrEach0`,
		`<!--r:`,
		`:` + itoa(eachID) + `-->`,
		`settled-empty`,
		`"await,omitempty"`,
		itoa(awaitID),
	} {
		if !strings.Contains(string(renderer), want) {
			t.Fatalf("settled keyed each SSR renderer misses %q:\n%s", want, renderer)
		}
	}

	client, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{
		`$$hawait` + itoa(eachID) + `_slot_keyed_each_range = $$acquireHydrationRange`,
		`$$adoptHydrationKeyedRowRanges`,
		`$$reconcileHydrationKeyedRowRanges`,
		`$$hawait` + itoa(eachID) + `_slot_keyed_each_controller`,
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("settled keyed each snapshot hydrate client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRRendererSelectsAwaitSnapshotKeyedEachDirectRowText(t *testing.T) {
	sfc, err := parseSFC(`<script>export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<p class="settled-row" :title={row.title} class:selected={row.selected} style:color={row.color} @click={() => globalThis.__velaRowClicks = (globalThis.__velaRowClicks || 0) + 1}>{row.title}</p>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("simple keyed row text snapshot was rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "id")`, `vela.Get(ssrEachItem0, "title")`, `vela.Get(ssrEachItem0, "selected")`, `vela.Get(ssrEachItem0, "color")`, `<!--v:`, `settled-row`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("dynamic keyed row SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`_find_text`, `createTreeWalker`, `$$writeHydrationText`, `createTextNode`, `$$rowItem`, `attrValue`, `getAttribute`, `setAttribute`, `classValue`, `classList.toggle`, `styleValue`, `style.setProperty`, `addEventListener`, `removeEventListener`} {
		if !strings.Contains(code, want) {
			t.Fatalf("dynamic keyed row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectInputBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<input class="settled-input" bind:value={row.title} />{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("keyed direct input snapshot was rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) || !ssrAwaitSnapshotSlotStaticKeyedEach(each) || !ssrAwaitSnapshotLeafBranch(await.ThenChildren, await.ValueName) {
		t.Fatalf("direct input snapshot predicates rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), `vela.Get(ssrEachItem0, "title")`) {
		t.Fatalf("direct input SSR renderer misses row value:\n%s", server)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`controlValue`, `addEventListener('input'`, `removeEventListener('input'`, `$$rowItem.title`, `$$set_rows(rows.slice())`} {
		if !strings.Contains(code, want) {
			t.Fatalf("direct input keyed-row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectInputBindingDeepState(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; let rows = $state.deep([]);</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<input bind:value={row.title} />{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	if !strings.Contains(code, `controlAssign: ($$value) => $$rowItem.title = $$value`) {
		t.Fatalf("deep direct input binding misses proxy member assignment:\n%s", code)
	}
	if strings.Contains(code, `$$set_rows(rows.slice())`) {
		t.Fatalf("deep direct input binding must not issue a shallow immutable setter:\n%s", code)
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectCheckboxBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<input type="checkbox" bind:checked={row.done} />{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("checkbox snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), `vela.Get(ssrEachItem0, "done")`) {
		t.Fatalf("direct checkbox SSR renderer misses row value:\n%s", server)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`controlValue`, `.checked`, `addEventListener('change'`, `removeEventListener('change'`, `$$set_rows(rows.slice())`} {
		if !strings.Contains(code, want) {
			t.Fatalf("direct checkbox keyed-row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectTextareaBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<textarea class="settled-textarea" bind:value={row.title} />{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("textarea snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), `vela.Get(ssrEachItem0, "title")`) {
		t.Fatalf("direct textarea SSR renderer misses row value:\n%s", server)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`createElement('textarea')`, `.value`, `addEventListener('input'`, `removeEventListener('input'`, `$$set_rows(rows.slice())`} {
		if !strings.Contains(code, want) {
			t.Fatalf("direct textarea keyed-row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select class="settled-select" bind:value={row.status}><optgroup label="Active"><option value="open">Open</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("select snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "status")`, `selected`, `optgroup`, `option value`, `open`, `done`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("direct select SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`createElement('select')`, `$$insertHTML($$row_element`, `controlApplied`, `controlDOMValue`, `addEventListener('change'`, `removeEventListener('change'`, `$$set_rows(rows.slice())`} {
		if !strings.Contains(code, want) {
			t.Fatalf("direct select keyed-row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptgroupLabel(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup :label={row.groupLabel}><option value="open">Open</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("dynamic optgroup label snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), `vela.Get(ssrEachItem0, "groupLabel")`) {
		t.Fatalf("dynamic optgroup label SSR renderer misses row member:\n%s", server)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('optgroup')`, `optionGroupDynamic`, `.label`, `$$insertHTML($$row_element`} {
		if !strings.Contains(code, want) {
			t.Fatalf("dynamic optgroup label hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptgroupLabelAndOptionDisabled(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup :label={row.groupLabel}><option value="open" :disabled={row.closed}>Open</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("composed optgroup/option snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "groupLabel")`, `vela.Get(ssrEachItem0, "closed")`, `" disabled"`, `selected`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("composed optgroup/option SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('optgroup')`, `querySelectorAll('option')`, `optionGroupDynamic`, `optionDynamic`, `.label`, `.disabled`} {
		if !strings.Contains(code, want) {
			t.Fatalf("composed optgroup/option hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptionDisabled(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Active"><option value="open" :disabled={row.closed}>Open</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("dynamic option snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "closed")`, `" disabled"`, `vela.Get(ssrEachItem0, "status")`, `optgroup`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("dynamic option SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('option')`, `optionDynamic`, `.disabled`, `$$insertHTML($$row_element`, `addEventListener('change'`} {
		if !strings.Contains(code, want) {
			t.Fatalf("dynamic option keyed-row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptionValue(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><option :value={row.openValue}>Open</option><option value="done">Done</option></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("dynamic option value snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "openValue")`, `vela.String`, `selected`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("dynamic option value SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('option')`, `optionDynamic`, `.value`, `$$insertHTML($$row_element`, `addEventListener('change'`} {
		if !strings.Contains(code, want) {
			t.Fatalf("dynamic option value keyed-row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptionText(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Active"><option value="open">Open: {row.label}</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("dynamic option text snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`Open: `, `vela.Get(ssrEachItem0, "label")`, `selected`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("dynamic option text SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('option')`, `optionText`, `.textContent`, `$$insertHTML($$row_element`, `addEventListener('change'`} {
		if !strings.Contains(code, want) {
			t.Fatalf("dynamic option text keyed-row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptgroupLabelAndOptionText(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup :label={row.groupLabel}><option value="open">Open: {row.label}</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("composed optgroup/text snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "groupLabel")`, `Open: `, `vela.Get(ssrEachItem0, "label")`, `selected`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("composed optgroup/text SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('optgroup')`, `querySelectorAll('option')`, `optionGroupDynamic`, `optionText`, `.label`, `.textContent`} {
		if !strings.Contains(code, want) {
			t.Fatalf("composed optgroup/text hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptionDisabledAndText(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Active"><option value="open" :disabled={row.closed}>Open: {row.label}</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("composed option attribute/text snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "closed")`, `" disabled"`, `Open: `, `vela.Get(ssrEachItem0, "label")`, `selected`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("composed option attribute/text SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('option')`, `optionDynamic`, `optionText`, `.disabled`, `.textContent`} {
		if !strings.Contains(code, want) {
			t.Fatalf("composed option attribute/text hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptgroupLabelOptionDisabledAndText(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup :label={row.groupLabel}><option value="open" :disabled={row.closed}>Open: {row.label}</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("triple-leaf optgroup snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "groupLabel")`, `vela.Get(ssrEachItem0, "closed")`, `" disabled"`, `Open: `, `vela.Get(ssrEachItem0, "label")`, `selected`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("triple-leaf optgroup SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('optgroup')`, `querySelectorAll('option')`, `optionGroupDynamic`, `optionDynamic`, `optionText`, `.label`, `.disabled`, `.textContent`} {
		if !strings.Contains(code, want) {
			t.Fatalf("triple-leaf optgroup hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptionList(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}>{#each row.options as option}<option :value={option.value}>{option.label}</option>{/each}</select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("dynamic option list snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.Get(ssrEachItem0, "options")`, `vela.Get(ssrEachItem1, "value")`, `vela.Get(ssrEachItem1, "label")`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("dynamic option list SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`createDocumentFragment`, `createElement('option')`, `replaceChildren`, `optionList`, `addEventListener('change'`} {
		if !strings.Contains(code, want) {
			t.Fatalf("dynamic option list keyed-row hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectOptgroupOptionList(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Available">{#each row.options as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("nested optgroup option list snapshot predicate rejected row=%#v", each.Children)
	}
	file, err := BuildSSRGoFile(plan, "page")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`optgroup`, `Available`, `vela.Get(ssrEachItem0, "options")`, `vela.Get(ssrEachItem1, "value")`, `vela.Get(ssrEachItem1, "label")`} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("nested optgroup option-list SSR renderer misses %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('optgroup')`, `createElement('optgroup')`, `createElement('option')`, `replaceChildren`, `optionList`} {
		if !strings.Contains(code, want) {
			t.Fatalf("nested optgroup option-list hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectKeyedOptgroupOptionList(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Available">{#each row.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("keyed nested optgroup option list snapshot predicate rejected row=%#v", each.Children)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`querySelectorAll('optgroup')`, `$$reconcileHydrationKeyedElements`, `optionBlocks`, `optionListAdopted`, `optionListKey`, `createElement('optgroup')`, `createElement('option')`} {
		if !strings.Contains(code, want) {
			t.Fatalf("keyed nested optgroup option-list hydration client misses %q:\n%s", want, code)
		}
	}
	if strings.Contains(code, `replaceChildren`) {
		t.Fatalf("keyed nested option list must retain option nodes rather than replace group children:\n%s", code)
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectDynamicOptgroupOptionList(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup :label={row.groupLabel}>{#each row.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="Archived"><option value="archived">Archived</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("dynamic optgroup keyed option-list snapshot predicate rejected row=%#v", each.Children)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`optionGroupDynamic`, `.label`, `$$reconcileHydrationKeyedElements`, `createElement('optgroup')`, `createElement('option')`} {
		if !strings.Contains(code, want) {
			t.Fatalf("dynamic optgroup option-list hydration client misses %q:\n%s", want, code)
		}
	}
	if !strings.Contains(code, `$$row_option_list_group_1`) {
		t.Fatalf("dynamic nested option-list hydration CSR builder misses static sibling optgroup:\n%s", code)
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectMultipleUnkeyedOptgroupOptionLists(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Available">{#each row.available as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="Archived">{#each row.archived as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("multiple unkeyed optgroup option-list snapshot predicate rejected row=%#v", each.Children)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`optionLists`, `option_list_creates`, `option_list_element_0`, `option_list_element_1`, `$$row_option_list_group_0`, `$$row_option_list_group_1`} {
		if !strings.Contains(code, want) {
			t.Fatalf("multiple unkeyed option-list hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectMultipleKeyedOptgroupOptionLists(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Available">{#each row.available as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="Archived">{#each row.archived as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("multiple keyed optgroup option-list snapshot predicate rejected row=%#v", each.Children)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`optionListKeys`, `optionBlocksByGroup`, `optionListAdoptedByGroup`, `option_list_reconciles`, `option_list_element_creates`, `option_list_element_updates`, `$$reconcileHydrationKeyedElements`} {
		if !strings.Contains(code, want) {
			t.Fatalf("multiple keyed option-list hydration client misses %q:\n%s", want, code)
		}
	}
	if strings.Contains(code, `replaceChildren`) {
		t.Fatalf("multiple keyed option lists must reconcile their owners rather than replace children:\n%s", code)
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectMultipleKeyedOptgroupOptionListsWithDynamicLabel(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup :label={row.availableLabel}>{#each row.available as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup :label={row.archivedLabel}>{#each row.archived as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("multiple keyed optgroup option-list snapshot predicate rejected dynamic group label row=%#v", each.Children)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`optionGroupDynamics`, `optgroup_dynamic_update_0`, `optgroup_dynamic_update_1`, `optionListKeys`, `optionBlocksByGroup`, `option_list_reconciles`, `$$reconcileHydrationKeyedElements`} {
		if !strings.Contains(code, want) {
			t.Fatalf("multiple keyed option-list dynamic-label hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectMixedOptgroupOptionLists(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Current">{#each row.current as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="History">{#each row.history as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`optionListKeys`, `optionBlocksByGroup`, `option_list_reconciles`, `$$reconcileHydrationKeyedElements`, `replaceChildren`} {
		if !strings.Contains(code, want) {
			t.Fatalf("mixed option-list hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectUnkeyedOptionListElse(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}><optgroup label="Results">{#each row.options as option}<option :value={option.value}>{option.label}</option>{:else}<option value="">No results</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`No results`, `$$insertHTML`, `replaceChildren`} {
		if !strings.Contains(code, want) {
			t.Fatalf("unkeyed option-list else hydration client misses %q:\n%s", want, code)
		}
	}
}

func TestGeneratedSSRHydrationAwaitSnapshotKeyedEachDirectSelectKeyedOptionList(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select bind:value={row.status}>{#each row.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	if !ssrAwaitSnapshotKeyedEachDirectControlPlanRow(each.Children, each.ItemName) {
		t.Fatalf("keyed option list snapshot predicate rejected row=%#v", each.Children)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(client, PrintPretty)
	for _, want := range []string{`$$reconcileHydrationKeyedElements`, `optionBlocks`, `optionListAdopted`, `optionListKey`, `createElement('option')`} {
		if !strings.Contains(code, want) {
			t.Fatalf("keyed option list hydration client misses %q:\n%s", want, code)
		}
	}
	if strings.Contains(code, `replaceChildren`) {
		t.Fatalf("keyed option list must retain option nodes rather than replace children:\n%s", code)
	}
}

func TestBrowserSSRHydrationAdoptsSettledAwaitSnapshotKeyedEachDirectRowText(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}{#each rows as row (row.id)}<p class="settled-row" :title={row.title} class:selected={row.selected} style:color={row.color} @click={() => globalThis.__velaRowClicks = (globalThis.__velaRowClicks || 0) + 1}>{row.title}</p>{/each}{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowText := rowElement.Children[0].(*SSRPlanText)
	rowScope := func(id string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + id }
	rowHTML := func(id, title, color string, selected bool) string {
		scope := rowScope(id)
		classes := "settled-row"
		if selected {
			classes += " selected"
		}
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><p vela-page="" class="` + classes + `" title="` + title + `" style="color:` + color + `"><!--v:` + scope + `:` + itoa(rowText.Parts[0].DynamicID) + `-->` + title + `<!--/v:` + scope + `:` + itoa(rowText.Parts[0].DynamicID) + `--></p><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "Alpha", "red", false) + rowHTML("b", "Beta", "blue", false) + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","title":"Alpha","selected":false,"color":"red"},{"id":"b","title":"Beta","selected":false,"color":"blue"}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.__velaRowClicks=0;window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("settled keyed await snapshot hydration mutated DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".settled-row")[0];window.b=document.querySelectorAll(".settled-row")[1];window.b.click()});await p.waitForFunction(()=>window.__velaRowClicks===1);await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",title:"Beta 2",selected:true,color:"green"},{id:"a",title:"Alpha",selected:false,color:"red"},{id:"c",title:"Gamma",selected:true,color:"purple"}]}));await p.waitForFunction(()=>document.querySelectorAll(".settled-row").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".settled-row")];window.b.click();return rows[0]===window.b&&rows[0].textContent==="Beta 2"&&rows[0].getAttribute("title")==="Beta 2"&&rows[0].classList.contains("selected")&&rows[0].style.color==="green"&&rows[1]===window.a&&!rows[1].classList.contains("selected")&&rows[1].style.color==="red"&&rows[2]!==window.a&&rows[2]!==window.b&&rows[2].getAttribute("title")==="Gamma"&&rows[2].classList.contains("selected")&&rows[2].style.color==="purple"&&window.__velaRowClicks===2})))throw new Error("settled keyed rows lost identity or direct dynamic values during reorder");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",title:"Beta 2",selected:true,color:"green"},{id:"c",title:"Gamma",selected:true,color:"purple"}]})});await p.waitForFunction(()=>document.querySelectorAll(".settled-row").length===2);if(!(await p.evaluate(()=>!window.removed.isConnected)))throw new Error("removed settled keyed row remained connected");await p.evaluate(()=>window.removed.click());if(await p.evaluate(()=>window.__velaRowClicks)!==2)throw new Error("removed settled keyed row listener remained active");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic settled keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic settled keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsSettledAwaitSnapshotKeyedEachDirectInputBinding(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}{#each rows as row (row.id)}<input class="settled-input" bind:value={row.title} />{/each}{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, title string) string {
		scope := rowScope(key)
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><input vela-page="" class="settled-input" value="` + title + `"><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "Alpha") + rowHTML("b", "Beta") + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","title":"Alpha"},{"id":"b","title":"Beta"}]},"state":{},"deep":["rows"],"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("input hydration wrote initial value");await p.evaluate(()=>{window.a=document.querySelectorAll(".settled-input")[0];window.b=document.querySelectorAll(".settled-input")[1];window.a.value="Alpha typed";window.a.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>document.querySelectorAll(".settled-input")[0].value==="Alpha typed");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",title:"Beta 2"},{id:"a",title:"Alpha typed"},{id:"c",title:"Gamma"}]}));await p.waitForFunction(()=>document.querySelectorAll(".settled-input").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".settled-input")];return rows[0]===window.b&&rows[0].value==="Beta 2"&&rows[1]===window.a&&rows[1].value==="Alpha typed"&&rows[2].value==="Gamma"})))throw new Error("keyed input rows lost identity or values");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",title:"Beta 2"},{id:"c",title:"Gamma"}]})});await p.waitForFunction(()=>document.querySelectorAll(".settled-input").length===2);await p.evaluate(()=>{window.removed.value="stale";window.removed.dispatchEvent(new Event("input",{bubbles:true}))});if(await p.evaluate(()=>window.removed.isConnected))throw new Error("removed input remained connected");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("direct input keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("direct input keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationDeepKeyedInputMutatesOnlyRetainedRow(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; let rows = $state.deep(window.__rows || []);</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}{#each rows as row (row.id)}<input class="settled-input" bind:value={row.title} />{/each}{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, title string) string {
		scope := rowScope(key)
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><input vela-page="" class="settled-input" value="` + title + `"><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "Alpha") + rowHTML("b", "Beta") + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"deep":["rows"],"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.__rows=[{id:"a",title:"Alpha"},{id:"b",title:"Beta"}];window.__rawRows=window.__rows;window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("deep input hydration wrote initial value");await p.evaluate(()=>{window.a=document.querySelectorAll(".settled-input")[0];window.b=document.querySelectorAll(".settled-input")[1];window.observer.takeRecords();window.a.value="Alpha deep";window.a.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>window.__rows[0].title==="Alpha deep");await p.evaluate(()=>new Promise(resolve=>setTimeout(resolve,0)));if(!(await p.evaluate(()=>window.__rows===window.__rawRows&&window.__rows[1].title==="Beta"&&document.querySelectorAll(".settled-input")[0]===window.a&&document.querySelectorAll(".settled-input")[1]===window.b&&window.a.value==="Alpha deep"&&window.b.value==="Beta"&&window.observer.takeRecords().length===0)))throw new Error("deep input write replaced rows or touched an unaffected retained row");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("deep keyed input await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("deep keyed input await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsSettledAwaitSnapshotKeyedEachDirectCheckboxBinding(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}{#each rows as row (row.id)}<input type="checkbox" class="settled-check" bind:checked={row.done} />{/each}{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key string, done bool) string {
		scope, checked := rowScope(key), ""
		if done {
			checked = " checked"
		}
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><input vela-page="" type="checkbox" class="settled-check"` + checked + `><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", true) + rowHTML("b", false) + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","done":true},{"id":"b","done":false}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("checkbox hydration wrote initial property");await p.evaluate(()=>{window.a=document.querySelectorAll(".settled-check")[0];window.b=document.querySelectorAll(".settled-check")[1];window.a.checked=false;window.a.dispatchEvent(new Event("change",{bubbles:true}))});await p.waitForFunction(()=>!document.querySelectorAll(".settled-check")[0].checked);await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",done:true},{id:"a",done:false},{id:"c",done:true}]}));await p.waitForFunction(()=>document.querySelectorAll(".settled-check").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".settled-check")];return rows[0]===window.b&&rows[0].checked&&rows[1]===window.a&&!rows[1].checked&&rows[2].checked})))throw new Error("keyed checkbox rows lost identity or checked values");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",done:true},{id:"c",done:true}]})});await p.waitForFunction(()=>document.querySelectorAll(".settled-check").length===2);await p.evaluate(()=>{window.removed.checked=true;window.removed.dispatchEvent(new Event("change",{bubbles:true}))});if(await p.evaluate(()=>window.removed.isConnected))throw new Error("removed checkbox remained connected");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("direct checkbox keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("direct checkbox keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsSettledAwaitSnapshotKeyedEachDirectTextareaBinding(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}{#each rows as row (row.id)}<textarea class="settled-textarea" bind:value={row.title} />{/each}{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, title string) string {
		scope := rowScope(key)
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><textarea vela-page="" class="settled-textarea">` + title + `</textarea><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "Alpha") + rowHTML("b", "Beta") + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","title":"Alpha"},{"id":"b","title":"Beta"}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("textarea hydration wrote initial value");await p.evaluate(()=>{window.a=document.querySelectorAll(".settled-textarea")[0];window.b=document.querySelectorAll(".settled-textarea")[1];window.a.value="Alpha typed";window.a.dispatchEvent(new Event("input",{bubbles:true}))});await p.waitForFunction(()=>document.querySelectorAll(".settled-textarea")[0].value==="Alpha typed");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",title:"Beta 2"},{id:"a",title:"Alpha typed"},{id:"c",title:"Gamma"}]}));await p.waitForFunction(()=>document.querySelectorAll(".settled-textarea").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".settled-textarea")];return rows[0]===window.b&&rows[0].value==="Beta 2"&&rows[1]===window.a&&rows[1].value==="Alpha typed"&&rows[2].value==="Gamma"})))throw new Error("keyed textarea rows lost identity or values");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",title:"Beta 2"},{id:"c",title:"Gamma"}]})});await p.waitForFunction(()=>document.querySelectorAll(".settled-textarea").length===2);await p.evaluate(()=>{window.removed.value="stale";window.removed.dispatchEvent(new Event("input",{bubbles:true}))});if(await p.evaluate(()=>window.removed.isConnected))throw new Error("removed textarea remained connected");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("direct textarea keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("direct textarea keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsSettledAwaitSnapshotKeyedEachDirectSelectBinding(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}{#each rows as row (row.id)}<select class="settled-select" bind:value={row.status}><optgroup :label={row.groupLabel}><option value="open" :disabled={row.closed}>Open</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, status, groupLabel string, closed bool) string {
		scope := rowScope(key)
		open, done, disabled := "", "", ""
		if status == "open" {
			open = " selected"
		} else if status == "done" {
			done = " selected"
		}
		if closed {
			disabled = " disabled"
		}
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><select vela-page="" class="settled-select"><optgroup label="` + groupLabel + `"><option value="open"` + disabled + open + `>Open</option></optgroup><optgroup label="Closed"><option value="done"` + done + `>Done</option></optgroup></select><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "open", "Active", true) + rowHTML("b", "done", "Working", false) + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","status":"open","groupLabel":"Active","closed":true},{"id":"b","status":"done","groupLabel":"Working","closed":false}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";const descriptor=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,"value");window.selectWrites=0;Object.defineProperty(HTMLSelectElement.prototype,"value",{configurable:true,get:descriptor.get,set(value){window.selectWrites++;return descriptor.set.call(this,value)}});window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial!==0||window.selectWrites!==0))throw new Error("select hydration wrote initial value");await p.evaluate(()=>{window.a=document.querySelectorAll(".settled-select")[0];window.b=document.querySelectorAll(".settled-select")[1];window.a.value="done";window.a.dispatchEvent(new Event("change",{bubbles:true}))});await p.waitForFunction(()=>document.querySelectorAll(".settled-select")[0].value==="done");await p.evaluate(()=>window.selectWrites=0);await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",status:"missing"},{id:"b",status:"done"}]}));await p.waitForFunction(()=>document.querySelectorAll(".settled-select")[0].value==="");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",status:"missing"},{id:"b",status:"done"}]}));await p.evaluate(()=>new Promise(resolve=>setTimeout(resolve,0)));if(await p.evaluate(()=>window.selectWrites)!==1)throw new Error("select normalized missing value was written more than once");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",status:"open"},{id:"a",status:"done"},{id:"c",status:"open"}]}));await p.waitForFunction(()=>document.querySelectorAll(".settled-select").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".settled-select")];return rows[0]===window.b&&rows[0].value==="open"&&rows[1]===window.a&&rows[1].value==="done"&&rows[2].value==="open"&&rows[2].options.length===2&&rows[2].querySelectorAll("optgroup").length===2})))throw new Error("keyed select rows lost identity, selection, or static option groups");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",status:"open"},{id:"c",status:"open"}]})});await p.waitForFunction(()=>document.querySelectorAll(".settled-select").length===2);await p.evaluate(()=>{window.removed.value="done";window.removed.dispatchEvent(new Event("change",{bubbles:true}))});if(await p.evaluate(()=>window.removed.isConnected))throw new Error("removed select remained connected");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	browser = strings.Replace(browser,
		`window.a=document.querySelectorAll(".settled-select")[0];window.b=document.querySelectorAll(".settled-select")[1];window.a.value="done"`,
		`window.a=document.querySelectorAll(".settled-select")[0];window.b=document.querySelectorAll(".settled-select")[1];window.aGroup=window.a.querySelector("optgroup");if(!window.a.options[0].disabled||window.b.options[0].disabled)throw new Error("server dynamic option state was not adopted");window.a.value="done"`, 1)
	browser = strings.ReplaceAll(browser, `{id:"a",status:"missing"}`, `{id:"a",status:"missing",groupLabel:"Archived",closed:false}`)
	browser = strings.ReplaceAll(browser, `{id:"b",status:"done"}`, `{id:"b",status:"done",groupLabel:"Working",closed:false}`)
	browser = strings.ReplaceAll(browser, `{id:"b",status:"open"}`, `{id:"b",status:"open",groupLabel:"Reopened",closed:false}`)
	browser = strings.ReplaceAll(browser, `{id:"a",status:"done"}`, `{id:"a",status:"done",groupLabel:"Archived",closed:true}`)
	browser = strings.ReplaceAll(browser, `{id:"c",status:"open"}`, `{id:"c",status:"open",groupLabel:"New",closed:true}`)
	browser = strings.Replace(browser,
		`document.querySelectorAll(".settled-select")[0].value===""`,
		`document.querySelectorAll(".settled-select")[0].value===""&&window.a.querySelector("optgroup")===window.aGroup&&window.aGroup.label==="Archived"&&window.a.options[0].disabled===false`, 1)
	browser = strings.Replace(browser,
		`rows[0]===window.b&&rows[0].value==="open"&&rows[1]===window.a&&rows[1].value==="done"`,
		`rows[0]===window.b&&rows[0].value==="open"&&rows[1]===window.a&&rows[1].value==="done"&&rows[1].querySelector("optgroup")===window.aGroup&&window.aGroup.label==="Archived"&&rows[1].options[0].disabled&&rows[2].options[0].disabled`, 1)
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("direct select keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("direct select keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationUpdatesSettledAwaitKeyedSelectOptgroupLabelOptionDisabledAndText(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select class="dynamic-select" bind:value={row.status}><optgroup :label={row.groupLabel}><option value="open" :disabled={row.closed}>Open: {row.label}</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowGroup := rowElement.Children[0].(*SSRPlanElement)
	rowOption := rowGroup.Children[0].(*SSRPlanElement)
	rowText := rowOption.Children[0].(*SSRPlanText)
	textID := rowText.Parts[1].DynamicID
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, status, label, groupLabel string, closed bool) string {
		scope, open, done, disabled := rowScope(key), "", "", ""
		if status == "open" {
			open = " selected"
		} else {
			done = " selected"
		}
		if closed {
			disabled = " disabled"
		}
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><select vela-page="" class="dynamic-select"><optgroup label="` + groupLabel + `"><option value="open"` + disabled + open + `>Open: <!--v:` + scope + `:` + itoa(textID) + `-->` + label + `<!--/v:` + scope + `:` + itoa(textID) + `--></option></optgroup><optgroup label="Closed"><option value="done"` + done + `>Done</option></optgroup></select><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "done", "Alpha", "Active", true) + rowHTML("b", "open", "Beta", "Working", false) + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","status":"done","label":"Alpha","groupLabel":"Active","closed":true},{"id":"b","status":"open","label":"Beta","groupLabel":"Working","closed":false}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial!==0))throw new Error("dynamic option hydration mutated server DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".dynamic-select")[0];window.b=document.querySelectorAll(".dynamic-select")[1]});if(!(await p.evaluate(()=>window.a.options[0].disabled&&window.b.options[0].disabled===false)))throw new Error("server option disabled state was not adopted");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",status:"done",closed:false},{id:"b",status:"open",closed:false}]}));await p.waitForFunction(()=>window.a.options[0].disabled===false);if(!(await p.evaluate(()=>document.querySelectorAll(".dynamic-select")[0]===window.a)))throw new Error("retained row lost identity while updating option disabled");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",status:"open",closed:false},{id:"a",status:"done",closed:true},{id:"c",status:"open",closed:true}]}));await p.waitForFunction(()=>document.querySelectorAll(".dynamic-select").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".dynamic-select")];return rows[0]===window.b&&rows[1]===window.a&&rows[1].options[0].disabled&&rows[2].options[0].disabled})))throw new Error("keyed option disabled rows lost identity or CSR initial value");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",status:"open",closed:false},{id:"c",status:"open",closed:true}]})});await p.waitForFunction(()=>document.querySelectorAll(".dynamic-select").length===2);await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	browser = strings.Replace(browser,
		`window.a=document.querySelectorAll(".dynamic-select")[0];window.b=document.querySelectorAll(".dynamic-select")[1]`,
		`window.a=document.querySelectorAll(".dynamic-select")[0];window.b=document.querySelectorAll(".dynamic-select")[1];window.aOption=window.a.options[0];window.aGroup=window.a.querySelector("optgroup")`, 1)
	browser = strings.Replace(browser,
		`window.a.options[0].disabled&&window.b.options[0].disabled===false`,
		`window.a.options[0].disabled&&window.a.options[0].textContent==="Open: Alpha"&&window.aGroup.label==="Active"&&window.b.options[0].disabled===false&&window.b.options[0].textContent==="Open: Beta"&&window.b.querySelector("optgroup").label==="Working"`, 1)
	browser = strings.Replace(browser,
		`{rows:[{id:"a",status:"done",closed:false},{id:"b",status:"open",closed:false}]}`,
		`{rows:[{id:"a",status:"done",label:"Aleph",groupLabel:"Archived",closed:false},{id:"b",status:"open",label:"Beta",groupLabel:"Working",closed:false}]}`, 1)
	browser = strings.Replace(browser,
		`window.a.options[0].disabled===false`,
		`window.a.options[0].disabled===false&&window.a.options[0].textContent==="Open: Aleph"&&window.aGroup.label==="Archived"`, 1)
	browser = strings.Replace(browser,
		`{rows:[{id:"b",status:"open",closed:false},{id:"a",status:"done",closed:true},{id:"c",status:"open",closed:true}]}`,
		`{rows:[{id:"b",status:"open",label:"Beta 2",groupLabel:"Reopened",closed:false},{id:"a",status:"done",label:"Aleph",groupLabel:"Archived",closed:true},{id:"c",status:"open",label:"Gamma",groupLabel:"New",closed:true}]}`, 1)
	browser = strings.Replace(browser,
		`rows[0]===window.b&&rows[1]===window.a&&rows[1].options[0].disabled&&rows[2].options[0].disabled`,
		`rows[0]===window.b&&rows[0].options[0].textContent==="Open: Beta 2"&&rows[0].querySelector("optgroup").label==="Reopened"&&rows[1]===window.a&&rows[1].options[0]===window.aOption&&rows[1].options[0].disabled&&rows[1].options[0].textContent==="Open: Aleph"&&rows[1].querySelector("optgroup")===window.aGroup&&window.aGroup.label==="Archived"&&rows[2].options[0].disabled&&rows[2].options[0].textContent==="Open: Gamma"&&rows[2].querySelector("optgroup").label==="New"`, 1)
	browser = strings.Replace(browser,
		`{rows:[{id:"b",status:"open",closed:false},{id:"c",status:"open",closed:true}]}`,
		`{rows:[{id:"b",status:"open",label:"Beta 2",groupLabel:"Reopened",closed:false},{id:"c",status:"open",label:"Gamma",groupLabel:"New",closed:true}]}`, 1)
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic option keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic option keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationUpdatesSettledAwaitKeyedSelectOptionValue(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select class="dynamic-value-select" bind:value={row.status}><option :value={row.openValue}>Open</option><option value="done">Done</option></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, openValue, status string) string {
		scope, open, done := rowScope(key), "", ""
		if status == openValue {
			open = " selected"
		} else {
			done = " selected"
		}
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><select vela-page="" class="dynamic-value-select"><option value="` + openValue + `"` + open + `>Open</option><option value="done"` + done + `>Done</option></select><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "alpha", "alpha") + rowHTML("b", "beta", "done") + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","openValue":"alpha","status":"alpha"},{"id":"b","openValue":"beta","status":"done"}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial!==0))throw new Error("dynamic option value hydration mutated server DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".dynamic-value-select")[0];window.b=document.querySelectorAll(".dynamic-value-select")[1]});if(!(await p.evaluate(()=>window.a.value==="alpha"&&window.a.options[0].value==="alpha"&&window.b.value==="done")))throw new Error("server dynamic option values were not adopted");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",openValue:"gamma",status:"gamma"},{id:"b",openValue:"beta",status:"done"}]}));await p.waitForFunction(()=>window.a.options[0].value==="gamma"&&window.a.value==="gamma");if(!(await p.evaluate(()=>document.querySelectorAll(".dynamic-value-select")[0]===window.a)))throw new Error("retained row lost identity while updating option value");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",openValue:"beta",status:"beta"},{id:"a",openValue:"delta",status:"done"},{id:"c",openValue:"new",status:"new"}]}));await p.waitForFunction(()=>document.querySelectorAll(".dynamic-value-select").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".dynamic-value-select")];return rows[0]===window.b&&rows[0].value==="beta"&&rows[1]===window.a&&rows[1].options[0].value==="delta"&&rows[1].value==="done"&&rows[2].options[0].value==="new"&&rows[2].value==="new"})))throw new Error("keyed dynamic option values lost ordering, identity, or CSR initial state");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",openValue:"beta",status:"beta"},{id:"c",openValue:"new",status:"new"}]})});await p.waitForFunction(()=>document.querySelectorAll(".dynamic-value-select").length===2);await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic option value keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic option value keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationUpdatesSettledAwaitKeyedSelectOptionText(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select class="dynamic-text-select" bind:value={row.status}><optgroup :label={row.groupLabel}><option value="open">Open: {row.label}</option></optgroup><optgroup label="Closed"><option value="done">Done</option></optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowGroup := rowElement.Children[0].(*SSRPlanElement)
	rowOption := rowGroup.Children[0].(*SSRPlanElement)
	rowText := rowOption.Children[0].(*SSRPlanText)
	textID := rowText.Parts[1].DynamicID
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	rowHTML := func(key, label, status, groupLabel string) string {
		scope, open, done := rowScope(key), "", ""
		if status == "open" {
			open = " selected"
		} else {
			done = " selected"
		}
		return `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><select vela-page="" class="dynamic-text-select"><optgroup label="` + groupLabel + `"><option value="open"` + open + `>Open: <!--v:` + scope + `:` + itoa(textID) + `-->` + label + `<!--/v:` + scope + `:` + itoa(textID) + `--></option></optgroup><optgroup label="Closed"><option value="done"` + done + `>Done</option></optgroup></select><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "Alpha", "open", "Active") + rowHTML("b", "Beta", "done", "Working") + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","label":"Alpha","status":"open","groupLabel":"Active"},{"id":"b","label":"Beta","status":"done","groupLabel":"Working"}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial!==0))throw new Error("dynamic option text hydration mutated server DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".dynamic-text-select")[0];window.b=document.querySelectorAll(".dynamic-text-select")[1]});if(!(await p.evaluate(()=>window.a.options[0].textContent==="Open: Alpha"&&window.b.options[0].textContent==="Open: Beta")))throw new Error("server dynamic option text was not adopted");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",label:"Aleph",status:"open"},{id:"b",label:"Beta",status:"done"}]}));await p.waitForFunction(()=>window.a.options[0].textContent==="Open: Aleph");if(!(await p.evaluate(()=>document.querySelectorAll(".dynamic-text-select")[0]===window.a)))throw new Error("retained row lost identity while updating option text");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",label:"Beta 2",status:"done"},{id:"a",label:"Aleph",status:"open"},{id:"c",label:"Gamma",status:"open"}]}));await p.waitForFunction(()=>document.querySelectorAll(".dynamic-text-select").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".dynamic-text-select")];return rows[0]===window.b&&rows[0].options[0].textContent==="Open: Beta 2"&&rows[1]===window.a&&rows[1].options[0].textContent==="Open: Aleph"&&rows[2].options[0].textContent==="Open: Gamma"})))throw new Error("keyed dynamic option text lost identity or CSR initial state");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",label:"Beta 2",status:"done"},{id:"c",label:"Gamma",status:"open"}]})});await p.waitForFunction(()=>document.querySelectorAll(".dynamic-text-select").length===2);await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	browser = strings.Replace(browser,
		`window.a=document.querySelectorAll(".dynamic-text-select")[0];window.b=document.querySelectorAll(".dynamic-text-select")[1]`,
		`window.a=document.querySelectorAll(".dynamic-text-select")[0];window.b=document.querySelectorAll(".dynamic-text-select")[1];window.aGroup=window.a.querySelector("optgroup")`, 1)
	browser = strings.ReplaceAll(browser, `{id:"a",label:"Aleph",status:"open"}`, `{id:"a",label:"Aleph",status:"open",groupLabel:"Archived"}`)
	browser = strings.ReplaceAll(browser, `{id:"b",label:"Beta",status:"done"}`, `{id:"b",label:"Beta",status:"done",groupLabel:"Working"}`)
	browser = strings.ReplaceAll(browser, `{id:"b",label:"Beta 2",status:"done"}`, `{id:"b",label:"Beta 2",status:"done",groupLabel:"Reopened"}`)
	browser = strings.ReplaceAll(browser, `{id:"c",label:"Gamma",status:"open"}`, `{id:"c",label:"Gamma",status:"open",groupLabel:"New"}`)
	browser = strings.Replace(browser,
		`window.a.options[0].textContent==="Open: Alpha"&&window.b.options[0].textContent==="Open: Beta"`,
		`window.a.options[0].textContent==="Open: Alpha"&&window.aGroup.label==="Active"&&window.b.options[0].textContent==="Open: Beta"&&window.b.querySelector("optgroup").label==="Working"`, 1)
	browser = strings.Replace(browser,
		`window.a.options[0].textContent==="Open: Aleph"`,
		`window.a.options[0].textContent==="Open: Aleph"&&window.aGroup.label==="Archived"`, 1)
	browser = strings.Replace(browser,
		`rows[0]===window.b&&rows[0].options[0].textContent==="Open: Beta 2"&&rows[1]===window.a&&rows[1].options[0].textContent==="Open: Aleph"&&rows[2].options[0].textContent==="Open: Gamma"`,
		`rows[0]===window.b&&rows[0].options[0].textContent==="Open: Beta 2"&&rows[0].querySelector("optgroup").label==="Reopened"&&rows[1]===window.a&&rows[1].options[0].textContent==="Open: Aleph"&&rows[1].querySelector("optgroup")===window.aGroup&&window.aGroup.label==="Archived"&&rows[2].options[0].textContent==="Open: Gamma"&&rows[2].querySelector("optgroup").label==="New"`, 1)
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic option text keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic option text keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationUpdatesSettledAwaitKeyedSelectOptgroupOptionList(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select class="nested-list-select" bind:value={row.status}><optgroup label="Available">{#each row.options as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="Archived">{#each row.archived as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	type option struct{ value, label string }
	rowHTML := func(key, status string, options, archived []option) string {
		scope := rowScope(key)
		body := `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><select vela-page="" class="nested-list-select"><optgroup vela-page="" label="Available">`
		for _, option := range options {
			selected := ""
			if option.value == status {
				selected = " selected"
			}
			body += `<option vela-page="" value="` + option.value + `"` + selected + `>` + option.label + `</option>`
		}
		body += `</optgroup><optgroup vela-page="" label="Archived">`
		for _, option := range archived {
			body += `<option vela-page="" value="` + option.value + `">` + option.label + `</option>`
		}
		return body + `</optgroup></select><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	aOptions := []option{{"open", "Open"}, {"done", "Done"}}
	bOptions := []option{{"later", "Later"}, {"done", "Done"}}
	aArchived, bArchived := []option{{"archived", "Archived"}}, []option{{"closed", "Closed"}}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "open", aOptions, aArchived) + rowHTML("b", "done", bOptions, bArchived) + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","status":"open","options":[{"value":"open","label":"Open"},{"value":"done","label":"Done"}],"archived":[{"value":"archived","label":"Archived"}]},{"id":"b","status":"done","options":[{"value":"later","label":"Later"},{"value":"done","label":"Done"}],"archived":[{"value":"closed","label":"Closed"}]}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial!==0))throw new Error("nested option list hydration mutated server DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".nested-list-select")[0];window.b=document.querySelectorAll(".nested-list-select")[1];window.aGroup=window.a.querySelectorAll("optgroup").item(0);window.bGroup=window.b.querySelectorAll("optgroup").item(0);window.aArchived=window.a.querySelectorAll("optgroup").item(1);window.bArchived=window.b.querySelectorAll("optgroup").item(1)});if(!(await p.evaluate(()=>window.a.value==="open"&&window.aGroup.querySelectorAll("option").length===2&&window.aArchived.querySelectorAll("option").length===1&&window.b.value==="done"&&window.bGroup.querySelectorAll("option")[0].textContent==="Later"&&window.bArchived.querySelectorAll("option")[0].textContent==="Closed")))throw new Error("nested server option lists were not adopted");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",status:"queued",options:[{value:"queued",label:"Queued"},{value:"done",label:"Done"}]},{id:"b",status:"done",options:[{value:"later",label:"Later"},{value:"done",label:"Done"}]}]}));await p.waitForFunction(()=>window.a.value==="queued"&&window.aGroup.querySelectorAll("option")[0].textContent==="Queued"&&window.aArchived.querySelectorAll("option").length===0);if(!(await p.evaluate(()=>document.querySelectorAll(".nested-list-select")[0]===window.a&&window.a.querySelectorAll("optgroup").item(0)===window.aGroup&&window.a.querySelectorAll("optgroup").item(1)===window.aArchived&&window.a.options.length===2)))throw new Error("nested option list replaced its owner rather than group children");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",status:"later",options:[{value:"later",label:"Later 2"},{value:"done",label:"Done"}]},{id:"a",status:"done",options:[{value:"queued",label:"Queued"},{value:"done",label:"Done 2"}]},{id:"c",status:"new",options:[{value:"new",label:"New"}]}]}));await p.waitForFunction(()=>document.querySelectorAll(".nested-list-select").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".nested-list-select")];return rows[0]===window.b&&rows[0].querySelectorAll("optgroup").item(0)===window.bGroup&&rows[0].querySelectorAll("optgroup").item(1)===window.bArchived&&rows[0].value==="later"&&rows[0].options[0].textContent==="Later 2"&&rows[1]===window.a&&rows[1].querySelectorAll("optgroup").item(0)===window.aGroup&&rows[1].querySelectorAll("optgroup").item(1)===window.aArchived&&rows[1].value==="done"&&rows[1].options[1].textContent==="Done 2"&&rows[2].querySelectorAll("optgroup").length===2&&rows[2].querySelectorAll("optgroup").item(0).label==="Available"&&rows[2].querySelectorAll("optgroup").item(1).label==="Archived"&&rows[2].value==="new"&&rows[2].options[0].textContent==="New"})))throw new Error("nested option list reorder or CSR insertion failed");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",status:"later",options:[{value:"later",label:"Later 2"},{value:"done",label:"Done"}]},{id:"c",status:"new",options:[{value:"new",label:"New"}]}]})});await p.waitForFunction(()=>document.querySelectorAll(".nested-list-select").length===2);if(await p.evaluate(()=>window.removed.isConnected))throw new Error("removed nested select remained connected");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested option-list keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("nested option-list keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationUpdatesSettledAwaitKeyedSelectMixedOptgroupOptionLists(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select class="dynamic-list-select" bind:value={row.status}><optgroup label="Available">{#each row.options as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="Archived">{#each row.archived as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	type option struct{ id, value, label string }
	rowHTML := func(key, status string, options, archived []option) string {
		scope := rowScope(key)
		body := `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><select vela-page="" class="dynamic-list-select"><optgroup vela-page="" label="Available">`
		for _, option := range options {
			selected := ""
			if option.value == status {
				selected = " selected"
			}
			body += `<option vela-page="" value="` + option.value + `"` + selected + `>` + option.label + `</option>`
		}
		body += `</optgroup><optgroup vela-page="" label="Archived">`
		for _, option := range archived {
			body += `<option vela-page="" value="` + option.value + `">` + option.label + `</option>`
		}
		return body + `</optgroup></select><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	aOptions, bOptions := []option{{"open", "open", "Open"}, {"done", "done", "Done"}}, []option{{"later", "later", "Later"}, {"done", "done", "Done"}}
	aArchived, bArchived := []option{{"archived", "archived", "Archived"}}, []option{{"closed", "closed", "Closed"}}
	// Keep server markup intentionally free of inner-list marker discovery:
	// the outer row controller owns the select as a single adopted element.
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "open", aOptions, aArchived) + rowHTML("b", "done", bOptions, bArchived) + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","status":"open","options":[{"id":"open","value":"open","label":"Open"},{"id":"done","value":"done","label":"Done"}],"archived":[{"id":"archived","value":"archived","label":"Archived"}]},{"id":"b","status":"done","options":[{"id":"later","value":"later","label":"Later"},{"id":"done","value":"done","label":"Done"}],"archived":[{"id":"closed","value":"closed","label":"Closed"}]}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial!==0))throw new Error("option list hydration mutated server DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".dynamic-list-select")[0];window.b=document.querySelectorAll(".dynamic-list-select")[1]});if(!(await p.evaluate(()=>window.a.value==="open"&&window.a.options.length===2&&window.b.value==="done"&&window.b.options[0].textContent==="Later")))throw new Error("server option lists were not adopted");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",status:"queued",options:[{value:"queued",label:"Queued"},{value:"done",label:"Done"}]},{id:"b",status:"done",options:[{value:"later",label:"Later"},{value:"done",label:"Done"}]}]}));await p.waitForFunction(()=>window.a.value==="queued"&&window.a.options[0].textContent==="Queued");if(!(await p.evaluate(()=>document.querySelectorAll(".dynamic-list-select")[0]===window.a&&window.a.options.length===2)))throw new Error("outer keyed row lost identity while replacing option list");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",status:"later",options:[{value:"later",label:"Later"},{value:"done",label:"Done"}]},{id:"a",status:"done",options:[{value:"queued",label:"Queued"},{value:"done",label:"Done"}]},{id:"c",status:"new",options:[{value:"new",label:"New"}]}]}));await p.waitForFunction(()=>document.querySelectorAll(".dynamic-list-select").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".dynamic-list-select")];return rows[0]===window.b&&rows[0].value==="later"&&rows[1]===window.a&&rows[1].value==="done"&&rows[2].value==="new"&&rows[2].options[0].textContent==="New"})))throw new Error("option list reorder or CSR insertion failed");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	browser = strings.Replace(browser,
		`window.a=document.querySelectorAll(".dynamic-list-select")[0];window.b=document.querySelectorAll(".dynamic-list-select")[1]`,
		`window.a=document.querySelectorAll(".dynamic-list-select")[0];window.b=document.querySelectorAll(".dynamic-list-select")[1];window.aGroup=window.a.querySelector("optgroup");window.bGroup=window.b.querySelector("optgroup");window.aArchivedGroup=window.a.querySelectorAll("optgroup").item(1);window.bArchivedGroup=window.b.querySelectorAll("optgroup").item(1);window.aOpen=window.a.options[0];window.aDone=window.a.options[1];window.aArchivedOption=window.aArchivedGroup.querySelector("option");window.bArchivedOption=window.bArchivedGroup.querySelector("option")`, 1)
	browser = strings.Replace(browser,
		`window.a.value==="open"&&window.a.options.length===2&&window.b.value==="done"&&window.b.options[0].textContent==="Later"`,
		`window.a.value==="open"&&window.aGroup.label==="Available"&&window.a.options.length===3&&window.aArchivedGroup.label==="Archived"&&window.aArchivedOption.textContent==="Archived"&&window.b.value==="done"&&window.bGroup.label==="Available"&&window.b.options.length===3&&window.bArchivedGroup.label==="Archived"&&window.bArchivedOption.textContent==="Closed"&&window.b.options[0].textContent==="Later"`, 1)
	browser = strings.Replace(browser,
		`options:[{value:"queued",label:"Queued"},{value:"done",label:"Done"}]},{id:"b",status:"done",options:[{value:"later",label:"Later"},{value:"done",label:"Done"}]}`,
		`options:[{id:"open",value:"queued",label:"Queued"},{id:"done",value:"done",label:"Done"}],archived:[{id:"archived",value:"archived",label:"Archived 2"}]},{id:"b",status:"done",options:[{id:"later",value:"later",label:"Later"},{id:"done",value:"done",label:"Done"}],archived:[{id:"closed",value:"closed",label:"Closed"}]}`, 1)
	browser = strings.Replace(browser,
		`options:[{value:"later",label:"Later"},{value:"done",label:"Done"}]},{id:"a",status:"done",options:[{value:"queued",label:"Queued"},{value:"done",label:"Done"}]},{id:"c",status:"new",options:[{value:"new",label:"New"}]}`,
		`options:[{id:"later",value:"later",label:"Later 2"},{id:"done",value:"done",label:"Done"}],archived:[{id:"closed",value:"closed",label:"Closed 2"}]},{id:"a",status:"done",options:[{id:"done",value:"done",label:"Done 2"},{id:"open",value:"queued",label:"Queued"}],archived:[{id:"archived",value:"archived",label:"Archived 2"}]},{id:"c",status:"new",options:[{id:"new",value:"new",label:"New"}],archived:[{id:"archived",value:"archived",label:"Archived"}]}`, 1)
	browser = strings.Replace(browser,
		`window.a.value==="queued"&&window.a.options[0].textContent==="Queued"`,
		`window.a.value==="queued"&&window.aGroup.label==="Available"&&window.a.options[0].textContent==="Queued"&&window.aArchivedGroup.querySelector("option")!==window.aArchivedOption&&window.aArchivedGroup.querySelector("option").textContent==="Archived 2"`, 1)
	browser = strings.Replace(browser,
		`document.querySelectorAll(".dynamic-list-select")[0]===window.a&&window.a.options.length===2`,
		`document.querySelectorAll(".dynamic-list-select")[0]===window.a&&window.a.querySelector("optgroup")===window.aGroup&&window.a.querySelectorAll("optgroup").item(1)===window.aArchivedGroup&&window.a.options.length===3&&window.a.options[0]===window.aOpen&&window.a.options[1]===window.aDone&&window.aArchivedGroup.querySelector("option")!==window.aArchivedOption`, 1)
	browser = strings.Replace(browser,
		`rows[0]===window.b&&rows[0].value==="later"&&rows[1]===window.a&&rows[1].value==="done"&&rows[2].value==="new"`,
		`rows[0]===window.b&&rows[0].querySelector("optgroup")===window.bGroup&&rows[0].querySelectorAll("optgroup").item(1)===window.bArchivedGroup&&rows[0].querySelector("optgroup").label==="Available"&&rows[0].querySelectorAll("optgroup").item(1).label==="Archived"&&rows[0].value==="later"&&rows[0].querySelectorAll("optgroup").item(1).querySelector("option")!==window.bArchivedOption&&rows[0].querySelectorAll("optgroup").item(1).querySelector("option").textContent==="Closed 2"&&rows[1]===window.a&&rows[1].querySelector("optgroup")===window.aGroup&&rows[1].querySelectorAll("optgroup").item(1)===window.aArchivedGroup&&rows[1].querySelector("optgroup").label==="Available"&&rows[1].querySelectorAll("optgroup").item(1).label==="Archived"&&rows[1].value==="done"&&rows[1].options[0]===window.aDone&&rows[1].options[1]===window.aOpen&&rows[1].querySelectorAll("optgroup").item(1).querySelector("option")!==window.aArchivedOption&&rows[2].querySelectorAll("optgroup").length===2&&rows[2].querySelector("optgroup").label==="Available"&&rows[2].querySelectorAll("optgroup").item(1).label==="Archived"&&rows[2].value==="new"&&rows[2].options[1].value==="archived"`, 1)
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic option list keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic option list keyed await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationUpdatesSettledAwaitKeyedSelectMixedOptgroupOptionListsWithDynamicLabels(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then value}{#each rows as row (row.id)}<select class="dynamic-keyed-label-select" bind:value={row.status}><optgroup :label={row.availableLabel}>{#each row.available as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup :label={row.archivedLabel}>{#each row.archived as option}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{:catch error}<p>Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	rowElement := each.Children[0].(*SSRPlanElement)
	rowScope := func(key string) string { return "vela-page--each-" + itoa(each.Span.Start) + "-" + key }
	type option struct{ id, value, label string }
	rowHTML := func(key, status, availableLabel, archivedLabel string, available, archived []option) string {
		scope := rowScope(key)
		body := `<!--r:` + scope + `:` + itoa(each.DynamicID) + `--><!--e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><select vela-page="" class="dynamic-keyed-label-select"><optgroup vela-page="" label="` + availableLabel + `">`
		for _, option := range available {
			selected := ""
			if option.value == status {
				selected = " selected"
			}
			body += `<option vela-page="" value="` + option.value + `"` + selected + `>` + option.label + `</option>`
		}
		body += `</optgroup><optgroup vela-page="" label="` + archivedLabel + `">`
		for _, option := range archived {
			body += `<option vela-page="" value="` + option.value + `">` + option.label + `</option>`
		}
		return body + `</optgroup></select><!--/e:` + scope + `:` + itoa(rowElement.HydrationID) + `--><!--/r:` + scope + `:` + itoa(each.DynamicID) + `-->`
	}
	aAvailable := []option{{"open", "open", "Open"}, {"done", "done", "Done"}}
	bAvailable := []option{{"later", "later", "Later"}, {"done", "done", "Done"}}
	aArchived, bArchived := []option{{"archived", "archived", "Archived"}}, []option{{"closed", "closed", "Closed"}}
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `-->` + rowHTML("a", "open", "Available", "Archived", aAvailable, aArchived) + rowHTML("b", "done", "Queued", "History", bAvailable, bArchived) + `<!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[{"id":"a","status":"open","availableLabel":"Available","archivedLabel":"Archived","available":[{"id":"open","value":"open","label":"Open"},{"id":"done","value":"done","label":"Done"}],"archived":[{"id":"archived","value":"archived","label":"Archived"}]},{"id":"b","status":"done","availableLabel":"Queued","archivedLabel":"History","available":[{"id":"later","value":"later","label":"Later"},{"id":"done","value":"done","label":"Done"}],"archived":[{"id":"closed","value":"closed","label":"Closed"}]}]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{"runtime.js": embeddedRuntime, "Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)), "Page.hydrate.js": []byte(PrintJS(entry, PrintPretty)), "index.html": []byte(html)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(fs.readFileSync(path.join(root,n)))}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial!==0))throw new Error("dynamic-label hydration mutated server DOM");await p.evaluate(()=>{window.a=document.querySelectorAll(".dynamic-keyed-label-select")[0];window.b=document.querySelectorAll(".dynamic-keyed-label-select")[1];window.aGroup=window.a.querySelector("optgroup");window.aArchivedGroup=window.a.querySelectorAll("optgroup").item(1);window.aOpen=window.aGroup.querySelectorAll("option").item(0);window.aArchived=window.aArchivedGroup.querySelector("option")});if(!(await p.evaluate(()=>window.aGroup.label==="Available"&&window.aArchivedGroup.label==="Archived"&&window.a.value==="open"&&window.b.querySelector("optgroup").label==="Queued"&&window.b.querySelectorAll("optgroup").item(1).label==="History")))throw new Error("dynamic labels or keyed option lists were not adopted");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"a",status:"done",availableLabel:"Active",archivedLabel:"Past",available:[{id:"done",value:"done",label:"Done 2"},{id:"open",value:"open",label:"Open"}],archived:[{id:"archived",value:"archived",label:"Archived 2"}]},{id:"b",status:"later",availableLabel:"Later",archivedLabel:"Closed",available:[{id:"later",value:"later",label:"Later"}],archived:[{id:"closed",value:"closed",label:"Closed"}]}]}));await p.waitForFunction(()=>window.aGroup.label==="Active"&&window.aArchivedGroup.label==="Past"&&window.a.value==="done"&&window.aArchived.textContent==="Archived 2");if(!(await p.evaluate(()=>window.aGroup.querySelectorAll("option").item(1)===window.aOpen&&window.aArchivedGroup.querySelector("option")===window.aArchived)))throw new Error("retained keyed options lost identity during dynamic label update");await p.evaluate(()=>window.hydrated.component.set({rows:[{id:"b",status:"later",availableLabel:"Reopened",archivedLabel:"History 2",available:[{id:"later",value:"later",label:"Later 2"}],archived:[{id:"closed",value:"closed",label:"Closed 2"}]},{id:"a",status:"open",availableLabel:"Done",archivedLabel:"Archive",available:[{id:"open",value:"open",label:"Open"},{id:"done",value:"done",label:"Done 2"}],archived:[{id:"archived",value:"archived",label:"Archived 2"}]},{id:"c",status:"new",availableLabel:"New",archivedLabel:"Old",available:[{id:"new",value:"new",label:"New"}],archived:[{id:"archived",value:"archived",label:"Archived"}]}]}));await p.waitForFunction(()=>document.querySelectorAll(".dynamic-keyed-label-select").length===3);if(!(await p.evaluate(()=>{const rows=[...document.querySelectorAll(".dynamic-keyed-label-select")];return rows[1]===window.a&&rows[1].querySelector("optgroup")===window.aGroup&&rows[1].querySelector("optgroup").label==="Done"&&rows[1].querySelectorAll("optgroup").item(1).label==="Archive"&&rows[1].querySelectorAll("optgroup").item(1).querySelector("option")===window.aArchived&&rows[2].querySelector("optgroup").label==="New"&&rows[2].querySelectorAll("optgroup").item(1).label==="Old"&&rows[2].value==="new"})))throw new Error("dynamic label keyed list reorder or CSR insertion failed");await p.evaluate(()=>{window.removed=window.a;window.hydrated.component.set({rows:[{id:"b",status:"later",availableLabel:"Reopened",archivedLabel:"History 2",available:[{id:"later",value:"later",label:"Later 2"}],archived:[{id:"closed",value:"closed",label:"Closed 2"}]},{id:"c",status:"new",availableLabel:"New",archivedLabel:"Old",available:[{id:"new",value:"new",label:"New"}],archived:[{id:"archived",value:"archived",label:"Archived"}]}]})});await p.waitForFunction(()=>document.querySelectorAll(".dynamic-keyed-label-select").length===2);if(await p.evaluate(()=>window.removed.isConnected))throw new Error("removed dynamic-label row remained connected");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left dynamic-label root");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	browser = strings.Replace(browser, `window.aArchivedGroup.querySelector("option")===window.aArchived`, `window.aArchivedGroup.querySelector("option")!==window.aArchived`, 1)
	browser = strings.Replace(browser, `window.aArchived.textContent==="Archived 2"`, `window.aArchivedGroup.querySelector("option").textContent==="Archived 2"`, 1)
	browser = strings.Replace(browser, `rows[1].querySelectorAll("optgroup").item(1).querySelector("option")===window.aArchived`, `rows[1].querySelectorAll("optgroup").item(1).querySelector("option")!==window.aArchived`, 1)
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic-label keyed option lists browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic-label keyed option lists browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsSettledAwaitSnapshotStaticKeyedEachElse(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let rows = [];</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}{#each rows as row (row)}<p class="settled-row">Row</p>{:else}<p class="settled-empty">Empty</p>{/each}{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	each := await.ThenChildren[0].(*SSRPlanEach)
	empty := each.ElseChildren[0].(*SSRPlanElement)
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main vela-page=""><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--v:vela-page:` + itoa(each.DynamicID) + `--><!--e:vela-page:` + itoa(empty.HydrationID) + `--><p vela-page="" class="settled-empty">Empty</p><!--/e:vela-page:` + itoa(empty.HydrationID) + `--><!--/v:vela-page:` + itoa(each.DynamicID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Rows":[]},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("empty settled keyed await snapshot hydration mutated DOM");await p.evaluate(()=>{window.empty=document.querySelector(".settled-empty");window.hydrated.component.set({rows:["a","b"]})});await p.waitForFunction(()=>document.querySelectorAll(".settled-row").length===2);if(await p.evaluate(()=>window.empty.isConnected))throw new Error("settled empty branch remained after rows were inserted");await p.evaluate(()=>window.hydrated.component.set({rows:[]}));await p.waitForFunction(()=>document.querySelectorAll(".settled-row").length===0&&document.querySelector(".settled-empty"));if(await p.evaluate(()=>document.querySelector(".settled-empty")===window.empty))throw new Error("settled empty branch reused detached DOM");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("settled empty keyed await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("settled empty keyed await snapshot browser output=%q", got)
	}
}

func TestGeneratedSSRRendererSelectsAwaitSnapshotChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request;</script><template><main>{#await request}<p>Pending</p>{:then value}<Child :title={value} /><p>Between</p><Child :title={value} /><p>Done</p>{:catch error}<Child :title={error} /><p>Between failed</p><Child :title={error} /><p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "parent")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`case "then":`,
		`child.ChildAt(child.ChildProps{Title: ssrAwaitSnapshot0.Value}, child.ChildSlots{}, instanceID+"--n0--n0--await-then--n0--component-Child")`,
		`child.ChildAt(child.ChildProps{Title: ssrAwaitSnapshot0.Value}, child.ChildSlots{}, instanceID+"--n0--n0--await-then--n2--component-Child")`,
	} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("SSR snapshot child renderer misses %q:\n%s", want, source)
		}
	}
	first := strings.Index(string(source), `await-then--n0--component-Child`)
	second := strings.Index(string(source), `await-then--n2--component-Child`)
	if first < 0 || second < first {
		t.Fatalf("SSR snapshot child chain does not preserve source order:\n%s", source)
	}
}

func TestGeneratedSSRRendererSelectsAwaitSnapshotBindableChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let draft = "server";</script><template><main>{#await request}<p>Pending</p>{:then value}<Child bind:value={draft} /><p>Done</p>{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "parent")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `child.Child(child.ChildProps{Value: props.Draft}, child.ChildSlots{})`) {
		t.Fatalf("SSR snapshot bindable child renderer misses bound prop:\n%s", source)
	}
}

func TestGeneratedSSRRendererSelectsAwaitSnapshotStaticSlotChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request;</script><template><main>{#await request}<p>Pending</p>{:then value}<Child><p class="slotted">Body</p></Child><p>Done</p>{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "parent")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`child.Child(child.ChildProps{}, child.ChildSlots{`,
		`Default: func(w io.Writer) error {`,
		`vela.WriteString(w, "<p class=\"slotted\">")`,
	} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("SSR snapshot static-slot child renderer misses %q:\n%s", want, source)
		}
	}
}

func TestGeneratedSSRRendererSelectsAwaitSnapshotReactiveSlotLeafChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let title = "server"; export let active = false; export let color = "red"; export let draft = "server"; export let enabled = false; function increment() {}</script><template><main>{#await request}<p>Pending</p>{:then value}<Child><p class="slotted" :title={title} class:active={active} style:color={color}>{title}</p><button @click={increment}>Increment</button><input bind:value={draft} /><input type="checkbox" bind:checked={enabled} /></Child><p>Done</p>{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "parent")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`child.Child(child.ChildProps{}, child.ChildSlots{`,
		`Default: func(w io.Writer) error {`,
		`vela.WriteString(w, "<p class=\"slotted")`,
		`vela.WriteString(w, ";\" title=\"")`,
		`vela.EscapeString(vela.String(props.Title))`,
		`if props.Active {`,
		`vela.String(props.Color)`,
		`data-vela-event=`,
	} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("SSR snapshot reactive-slot child renderer misses %q:\n%s", want, source)
		}
	}
}

func TestGeneratedSSRRendererAndHydratorSelectAwaitSnapshotProjectedChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; import { Leaf } from "./Leaf.vela"; export let request; export let title = "server"; export let active = false; export let color = "red"; export let draft = "server"; export let enabled = false;</script><template><main>{#await request}<p>Pending</p>{:then value}<Outer><Inner :title={title}><p class="inner-slot" :title={title} class:active={active} style:color={color}>{title}</p><Leaf :title={title} /><input bind:value={draft} /><input type="checkbox" bind:checked={enabled} /><svelte:fragment slot="actions"><button>Action</button></svelte:fragment></Inner></Outer><p>Done</p>{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	capability := AnalyzeSSRCapability(sfc.TemplateAST.Nodes)
	if !capability.ExecutableHydration() {
		issue, _ := capability.FirstHydrationIssue()
		t.Fatalf("projected snapshot child capability=%#v", issue)
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
		"Leaf":  {LocalName: "Leaf", SourcePath: "Leaf.vela", GoName: "Leaf", PackageName: "leaf", ImportPath: "example.com/project/leaf"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "parent")
	if err != nil {
		t.Fatal(err)
	}
	server, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`inner.Inner(inner.InnerProps{Title: props.Title}, inner.InnerSlots{`,
		`Default: func(w io.Writer) error {`,
		`Actions: func(w io.Writer) error {`,
		`class=\"inner-slot`,
		`vela.EscapeString(vela.String(props.Title))`,
		`if props.Active {`,
		`vela.String(props.Color)`,
		`vela.String(props.Draft)`,
		`if props.Enabled {`,
		`leaf.Leaf(leaf.LeafProps{Title: props.Title}, leaf.LeafSlots{})`,
	} {
		if !strings.Contains(string(server), want) {
			t.Fatalf("SSR renderer misses projected child static slot %q:\n%s", want, server)
		}
	}
	client, err := BuildSSRHydrationClientProgram("Parent", sfc, analysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	generated := PrintJS(client, PrintPretty)
	for _, want := range []string{
		`from './Outer.hydrate.js'`,
		`from './Inner.hydrate.js'`,
		`from './Inner.js'`,
		`from './Leaf.hydrate.js'`,
		`from './Leaf.js'`,
		`$$csr_Inner`,
		`$$csr_Leaf`,
		`Inner($$root).component`,
		`Leaf($$root).component`,
		`$$csr_Inner($$n`,
	} {
		if !strings.Contains(generated, want) {
			t.Fatalf("hydration client misses %q:\n%s", want, generated)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildSupportsEventAndBindable(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; let selected = $state("server"); let closes = $state(0); function closed() { closes++; }</script><template><main>{#await request}<p>Pending</p>{:then value}<Outer><Inner bind:value={selected} @on:close={closed}><p>{selected}</p></Inner></Outer><p>Done</p>{:catch error}<p>Failed</p>{/await}<p>{selected}:{closes}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("projected await snapshot child event/binding shape was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`Inner($$root, { $$onChange_value: ($$v) => $$set_selected($$v) }).component`,
		`addEventListener('close'`,
		`removeEventListener('close'`,
		`$$set_selected`,
		`$$set_closes`,
		`$$csr_Inner`,
		`$$writeHydrationText`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected snapshot child event/binding lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotSupportsDOMEvent(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; function click() {}</script><template><main>{#await request}<p>Pending</p>{:then value}<Outer><Inner><button @click={click}>Click</button></Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("DOM event in projected-child own slot was rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{`addEventListener('click'`, `removeEventListener('click'`} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected child own-slot event lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotSupportsNativeBindings(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; export let draft = ""; export let enabled = false;</script><template><main>{#await request}<p>Pending</p>{:then result}<Outer><Inner><input bind:value={draft} /><input type="checkbox" bind:checked={enabled} /></Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("native bindings in projected-child own slot were rejected")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{`addEventListener('input'`, `removeEventListener('input'`, `addEventListener('change'`, `removeEventListener('change'`, `$$set_draft`, `$$set_enabled`} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected child own-slot binding lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotSupportsStaticIf(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; export let show = true; export let later = false;</script><template><main>{#await request}<p>Pending</p>{:then result}<Outer><Inner>{#if show}<p class="slot-on">On</p>{:else if later}<p class="slot-later">Later</p>{:else}<p class="slot-off">Off</p>{/if}</Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static #if in projected-child own slot was rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSSRGoFile(plan, "parent"); err != nil {
		t.Fatalf("SSR renderer rejected projected-child own slot #if: %v", err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{`$$acquireHydrationRange`, `$$adoptHydrationRange`, `$$insertHTML`, `slot-on`, `slot-later`, `slot-off`, `updates: []`, `listeners: []`, `children: []`, `adopt: () =>`, `mountCSR: (`, `update: () =>`, `destroy: () =>`} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected child own-slot #if lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotIfUpdatesDirectText(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; export let show = true; export let title = "server";</script><template><main>{#await request}<p>Pending</p>{:then result}<Outer><Inner>{#if show}{title}{:else}Off{/if}</Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("direct text in projected-child own-slot #if was rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSSRGoFile(plan, "parent"); err != nil {
		t.Fatalf("SSR renderer rejected projected-child own-slot dynamic #if text: %v", err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{`$$writeHydrationText`, `$$acquireHydrationRange`, `updates.push`, `createTextNode`, `Off`} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected child own-slot dynamic #if text lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotIfUpdatesDirectElement(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; export let show = true; export let title = "server"; export let active = false; export let color = "red"; function branchClicked() {}</script><template><main>{#await request}<p>Pending</p>{:then result}<Outer><Inner>{#if show}<p :title={title} class:selected={active} style:color={color} @click={branchClicked}>Branch</p>{:else}<p>Off</p>{/if}</Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("direct element in projected-child own-slot #if was rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSSRGoFile(plan, "parent"); err != nil {
		t.Fatalf("SSR renderer rejected projected-child own-slot dynamic #if element: %v", err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{`$$acquireHydrationElement`, `getAttribute('title')`, `classList.toggle('selected'`, `style.setProperty('color'`, `createElement('p')`, `addEventListener('click', branchClicked)`, `removeEventListener('click', branchClicked)`, `Branch`} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected child own-slot dynamic #if element lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotIfOwnsNativeBindings(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; export let show = true; export let draft = "server"; export let enabled = false;</script><template><main>{#await request}<p>Pending</p>{:then result}<Outer><Inner>{#if show}<input type="text" bind:value={draft} /><input type="checkbox" bind:checked={enabled} />{:else}<p>Off</p>{/if}</Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("native bindings in projected-child own-slot #if were rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSSRGoFile(plan, "parent"); err != nil {
		t.Fatalf("SSR renderer rejected projected-child own-slot #if bindings: %v", err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{`$$set_draft`, `$$set_enabled`, `addEventListener('input'`, `addEventListener('change'`, `removeEventListener('input'`, `removeEventListener('change'`, `updates.push`} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected child own-slot #if binding lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotStaticKey(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; export let identity = "server";</script><template><main>{#await request}<p>Pending</p>{:then result}<Outer><Inner>{#key identity}<p class="keyed">Stable branch</p>{/key}</Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static #key in projected-child own slot was rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSSRGoFile(plan, "parent"); err != nil {
		t.Fatalf("SSR renderer rejected projected-child own-slot static #key: %v", err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{`$$acquireHydrationRange`, `$$adoptHydrationRange`, `$$insertHTML`, `key: identity`, `Stable branch`} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected child own-slot #key lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotStaticEach(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then result}<Outer><Inner>{#each rows as row}<p class="row">Row</p>{:else}<p class="empty">Empty</p>{/each}</Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static unkeyed projected-slot #each was rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSSRGoFile(plan, "parent"); err != nil {
		t.Fatalf("SSR renderer rejected projected-slot static each: %v", err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{`$$adoptHydrationRange`, `for (const $$item of`, `$$insertHTML`, `Empty`} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected-slot static each lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationAwaitSnapshotProjectedChildOwnSlotStaticKeyedEach(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; export let request; export let rows = [];</script><template><main>{#await request}<p>Pending</p>{:then result}<Outer><Inner>{#each rows as row (row)}<p class="row">Row</p><span class="detail">Static</span>{/each}</Inner></Outer>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static keyed projected-slot #each was rejected")
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSSRGoFile(plan, "parent"); err != nil {
		t.Fatalf("SSR renderer rejected projected-slot static keyed each: %v", err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`$$adoptHydrationKeyedRowRanges`,
		`$$reconcileHydrationKeyedRowRanges`,
		`createComment('r')`,
		`createComment('/r')`,
		`controller: $$controller`,
		`children: []`,
		`listeners.slice().reverse()`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("projected-slot static keyed each lowering misses %q:\n%s", want, source)
		}
	}
}

func TestBrowserSSRHydrationProjectedSlotStaticKeyedEachRows(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<template><section class="projected-keyed-child"><slot /></section></template>`)
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

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let rows = []; function rowClicked() { window.projectedKeyedClicks = (window.projectedKeyedClicks || 0) + 1; }</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<Child>{#each rows as row (row)}<button class="projected-keyed-row" @click={rowClicked}>Row</button><span class="projected-keyed-detail">Static</span>{/each}</Child>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", parentSFC, parentAnalysis, "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", SourcePath: "Child.vela", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
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

	parentRoot := parentPlan.Nodes[0].(*SSRPlanElement)
	awaitPlan := parentRoot.Children[0].(*SSRPlanAwait)
	componentPlan := awaitPlan.ThenChildren[0].(*SSRPlanComponent)
	eachPlan := componentPlan.Slots["default"][0].(*SSRPlanEach)
	rowElement := eachPlan.Children[0].(*SSRPlanElement)
	detailElement := eachPlan.Children[1].(*SSRPlanElement)
	awaitNode := parentSFC.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*AwaitBlockNode)
	componentNode := awaitNode.ThenChildren[0].(*ComponentNode)
	childSuffix := parentPlan.ComponentInstanceSuffixes[componentNode.Span]
	childScope := childPlan.ScopeID
	if childSuffix != "" {
		childScope = parentPlan.ScopeID + "--" + childSuffix
	}
	childRoot := childPlan.Nodes[0].(*SSRPlanElement)
	rowHTML := func() string {
		return `<!--r:vela-parent:` + itoa(eachPlan.DynamicID) + `--><!--e:vela-parent:` + itoa(rowElement.HydrationID) + `--><button vela-parent="" class="projected-keyed-row" data-vela-event="vela-parent-e` + itoa(rowElement.HydrationID) + `">Row</button><!--/e:vela-parent:` + itoa(rowElement.HydrationID) + `--><!--e:vela-parent:` + itoa(detailElement.HydrationID) + `--><span vela-parent="" class="projected-keyed-detail">Static</span><!--/e:vela-parent:` + itoa(detailElement.HydrationID) + `--><!--/r:vela-parent:` + itoa(eachPlan.DynamicID) + `-->`
	}
	html := `<!doctype html><body><!--e:vela-parent:` + itoa(parentRoot.HydrationID) + `--><main vela-parent=""><!--v:vela-parent:` + itoa(awaitPlan.DynamicID) + `--><!--e:` + childScope + `:` + itoa(childRoot.HydrationID) + `--><section vela-child="" class="projected-keyed-child"><!--v:vela-parent:` + itoa(eachPlan.DynamicID) + `-->` + rowHTML() + rowHTML() + `<!--/v:vela-parent:` + itoa(eachPlan.DynamicID) + `--></section><!--/e:` + childScope + `:` + itoa(childRoot.HydrationID) + `--><script type="application/json" id="vela-state-` + childScope + `">{"component":"` + childScope + `","props":{},"state":{},"incomplete":false}</script><!--/v:vela-parent:` + itoa(awaitPlan.DynamicID) + `--></main><!--/e:vela-parent:` + itoa(parentRoot.HydrationID) + `--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Rows":["a","b"]},"state":{},"incomplete":false,"await":{"` + itoa(awaitPlan.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.projectedKeyedClicks=0;window.request=new Promise(()=>{});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydrateParent(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`

	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("projected keyed hydration mutated DOM");await p.evaluate(()=>{window.rows=[...document.querySelectorAll(".projected-keyed-row")];window.details=[...document.querySelectorAll(".projected-keyed-detail")];window.rows[0].click()});await p.waitForFunction(()=>window.projectedKeyedClicks===1);await p.evaluate(()=>window.hydrated.component.set({rows:["b","a"]}));await p.waitForFunction(()=>document.querySelectorAll(".projected-keyed-row").length===2);const reordered=await p.evaluate(()=>{const rows=[...document.querySelectorAll(".projected-keyed-row")],details=[...document.querySelectorAll(".projected-keyed-detail")];return rows[0]===window.rows[1]&&rows[1]===window.rows[0]&&details[0]===window.details[1]&&details[1]===window.details[0]});if(!reordered)throw new Error("keyed row ranges were not retained during reorder");await p.evaluate(()=>window.hydrated.component.set({rows:["b","c","a"]}));await p.waitForFunction(()=>document.querySelectorAll(".projected-keyed-row").length===3);const inserted=await p.evaluate(()=>{const rows=[...document.querySelectorAll(".projected-keyed-row")];window.removed=rows[0];window.retained=rows[2];rows[1].click();return rows[0]===window.rows[1]&&rows[2]===window.rows[0]&&rows[1]!==window.rows[0]&&rows[1]!==window.rows[1]});if(!inserted)throw new Error("keyed insertion did not preserve retained rows");await p.waitForFunction(()=>window.projectedKeyedClicks===2);await p.evaluate(()=>window.hydrated.component.set({rows:["c","a"]}));await p.waitForFunction(()=>document.querySelectorAll(".projected-keyed-row").length===2);await p.evaluate(async()=>{window.removed.click();await new Promise(resolve=>setTimeout(resolve,0));window.afterDetached=window.projectedKeyedClicks});const result=await p.evaluate(()=>({initial:window.initial,removedDetached:!window.removed.isConnected,retained:document.querySelectorAll(".projected-keyed-row")[1]===window.retained,details:document.querySelectorAll(".projected-keyed-detail").length,child:!!document.querySelector(".projected-keyed-child"),afterDetached:window.afterDetached}));if(result.initial!==0||!result.removedDetached||!result.retained||result.details!==2||!result.child||result.afterDetached!==2)throw new Error(JSON.stringify(result));await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("projected keyed each browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("projected keyed each browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsAwaitSnapshotProjectedChild(t *testing.T) {
	node := requirePlaywright(t)
	outerSFC, err := parseSFC(`<template><section class="snapshot-outer"><slot /></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	innerSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; export let value = $bindable(""); function clicked() { window.snapshotInnerClicks = (window.snapshotInnerClicks || 0) + 1; } function close() { $$dispatch("close"); } onDestroy(() => { window.snapshotInnerDestroys = (window.snapshotInnerDestroys || 0) + 1; });</script><template><section class="snapshot-inner-wrap"><input class="snapshot-inner-value" bind:value={value} /><button class="snapshot-inner-close" @click={close}>Close</button><button class="snapshot-inner" @click={clicked}>{title}</button><slot /><slot name="actions" /></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	leafSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.snapshotLeafDestroys = (window.snapshotLeafDestroys || 0) + 1; window.snapshotLeafInnerAlive = !!document.querySelector(".snapshot-inner-wrap")?.isConnected; });</script><template><button class="snapshot-leaf">{title}</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	outerAnalysis := analyzeScriptAST(outerSFC.ScriptAST)
	innerAnalysis := analyzeScriptAST(innerSFC.ScriptAST)
	leafAnalysis := analyzeScriptAST(leafSFC.ScriptAST)
	outerPlan, err := BuildSSRRenderPlanWithAnalysis("Outer", outerSFC, outerAnalysis, "vela-outer")
	if err != nil {
		t.Fatal(err)
	}
	innerPlan, err := BuildSSRRenderPlanWithAnalysis("Inner", innerSFC, innerAnalysis, "vela-inner")
	if err != nil {
		t.Fatal(err)
	}
	leafPlan, err := BuildSSRRenderPlanWithAnalysis("Leaf", leafSFC, leafAnalysis, "vela-leaf")
	if err != nil {
		t.Fatal(err)
	}
	outerClient, err := BuildSSRHydrationClientProgram("Outer", outerSFC, outerAnalysis, "vela-outer")
	if err != nil {
		t.Fatal(err)
	}
	innerClient, err := BuildSSRHydrationClientProgram("Inner", innerSFC, innerAnalysis, "vela-inner")
	if err != nil {
		t.Fatal(err)
	}
	leafClient, err := BuildSSRHydrationClientProgram("Leaf", leafSFC, leafAnalysis, "vela-leaf")
	if err != nil {
		t.Fatal(err)
	}
	outerEntry, err := BuildSSRHydrateEntryWithClient(outerPlan, "./Outer.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	innerEntry, err := BuildSSRHydrateEntryWithClient(innerPlan, "./Inner.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	leafEntry, err := BuildSSRHydrateEntryWithClient(leafPlan, "./Leaf.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	outerCSR, _ := buildProgramWithDOMAcquirePlan("Outer", outerSFC, outerSFC.TemplateAST.Nodes, outerAnalysis, "vela-outer", false, DOMAcquireCreate)
	innerCSR, _ := buildProgramWithDOMAcquirePlan("Inner", innerSFC, innerSFC.TemplateAST.Nodes, innerAnalysis, "vela-inner", false, DOMAcquireCreate)
	leafCSR, _ := buildProgramWithDOMAcquirePlan("Leaf", leafSFC, leafSFC.TemplateAST.Nodes, leafAnalysis, "vela-leaf", false, DOMAcquireCreate)

	parentSFC, err := parseSFC(`<script>import { Outer } from "./Outer.vela"; import { Inner } from "./Inner.vela"; import { Leaf } from "./Leaf.vela"; export let request; export let title = "server"; export let active = false; export let color = "red"; export let show = true; export let elementShown = true; export let draft = "server"; export let enabled = false; let selected = $state("server"); function closed() { window.snapshotBound = selected; window.snapshotCloses = (window.snapshotCloses || 0) + 1; } function slotClicked() { window.snapshotSlotClicks = (window.snapshotSlotClicks || 0) + 1; window.snapshotSlotDraft = draft; window.snapshotSlotEnabled = enabled; }</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<Outer><Inner :title={title} bind:value={selected} @on:close={closed}><p class="snapshot-inner-slot" :title={title} class:active={active} style:color={color} @click={slotClicked}>{title}</p><Leaf :title={title} />{#if show}Value: {title}{:else}Off{/if}{#if elementShown}<p class="snapshot-inner-branch" :title={title} class:active={active} style:color={color}>Branch</p>{:else}<p class="snapshot-inner-branch-off">Branch off</p>{/if}<input class="snapshot-inner-slot-value" bind:value={draft} /><input class="snapshot-inner-slot-enabled" type="checkbox" bind:checked={enabled} /><svelte:fragment slot="actions"><button class="snapshot-inner-action">Action</button></svelte:fragment></Inner></Outer><p class="settled">Done</p>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", parentSFC, parentAnalysis, "vela-parent", map[string]SSRComponentLink{
		"Outer": {LocalName: "Outer", SourcePath: "Outer.vela", GoName: "Outer", PackageName: "outer", ImportPath: "example.com/project/outer"},
		"Inner": {LocalName: "Inner", SourcePath: "Inner.vela", GoName: "Inner", PackageName: "inner", ImportPath: "example.com/project/inner"},
		"Leaf":  {LocalName: "Leaf", SourcePath: "Leaf.vela", GoName: "Leaf", PackageName: "leaf", ImportPath: "example.com/project/leaf"},
	})
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
	parentRoot := parentPlan.Nodes[0].(*SSRPlanElement)
	awaitPlan := parentRoot.Children[0].(*SSRPlanAwait)
	outerComponent := awaitPlan.ThenChildren[0].(*SSRPlanComponent)
	innerComponent := outerComponent.Slots["default"][0].(*SSRPlanComponent)
	awaitNode := parentSFC.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*AwaitBlockNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(awaitNode.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	outerRoot := outerPlan.Nodes[0].(*SSRPlanElement)
	innerRoot := innerPlan.Nodes[0].(*SSRPlanElement)
	innerInput := innerRoot.Children[0].(*SSRPlanElement)
	innerClose := innerRoot.Children[1].(*SSRPlanElement)
	innerButton := innerRoot.Children[2].(*SSRPlanElement)
	innerText := innerButton.Children[0].(*SSRPlanText).Parts[0]
	innerSlot := innerComponent.Slots["default"][0].(*SSRPlanElement)
	innerSlotText := innerSlot.Children[0].(*SSRPlanText).Parts[0]
	leafComponent := innerComponent.Slots["default"][1].(*SSRPlanComponent)
	innerSlotIf := innerComponent.Slots["default"][2].(*SSRPlanIf)
	innerSlotIfText := innerSlotIf.Branches[0].Children[0].(*SSRPlanText).Parts[1]
	innerSlotElementIf := innerComponent.Slots["default"][3].(*SSRPlanIf)
	innerSlotElement := innerSlotElementIf.Branches[0].Children[0].(*SSRPlanElement)
	innerSlotInput := innerComponent.Slots["default"][4].(*SSRPlanElement)
	innerSlotEnabled := innerComponent.Slots["default"][5].(*SSRPlanElement)
	leafRoot := leafPlan.Nodes[0].(*SSRPlanElement)
	leafText := leafRoot.Children[0].(*SSRPlanText).Parts[0]
	if outerComponent.Link == nil || innerComponent.Link == nil || leafComponent.Link == nil {
		t.Fatal("projected component links are missing")
	}
	html := `<!doctype html><body><!--e:vela-parent:` + itoa(parentRoot.HydrationID) + `--><main><!--v:vela-parent:` + itoa(awaitID) + `--><!--e:vela-outer:` + itoa(outerRoot.HydrationID) + `--><section class="snapshot-outer"><!--e:vela-inner:` + itoa(innerRoot.HydrationID) + `--><section class="snapshot-inner-wrap"><!--e:vela-inner:` + itoa(innerInput.HydrationID) + `--><input class="snapshot-inner-value" value="server"><!--/e:vela-inner:` + itoa(innerInput.HydrationID) + `--><!--e:vela-inner:` + itoa(innerClose.HydrationID) + `--><button class="snapshot-inner-close">Close</button><!--/e:vela-inner:` + itoa(innerClose.HydrationID) + `--><!--e:vela-inner:` + itoa(innerButton.HydrationID) + `--><button class="snapshot-inner"><!--v:vela-inner:` + itoa(innerText.DynamicID) + `-->server<!--/v:vela-inner:` + itoa(innerText.DynamicID) + `--></button><!--/e:vela-inner:` + itoa(innerButton.HydrationID) + `--></section><!--/e:vela-inner:` + itoa(innerRoot.HydrationID) + `--><script type="application/json" id="vela-state-vela-inner">{"component":"vela-inner","props":{"Title":"server","Value":"server"},"state":{},"incomplete":false}</script></section><!--/e:vela-outer:` + itoa(outerRoot.HydrationID) + `--><script type="application/json" id="vela-state-vela-outer">{"component":"vela-outer","props":{},"state":{},"incomplete":false}</script><p class="settled">Done</p><!--/v:vela-parent:` + itoa(awaitID) + `--></main><!--/e:vela-parent:` + itoa(parentRoot.HydrationID) + `--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Title":"server"},"state":{},"incomplete":false,"await":{"` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.snapshotInnerClicks=0;window.snapshotInnerDestroys=0;window.snapshotCloses=0;window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydrateParent(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	innerStaticSlots := `<!--e:vela-parent:` + itoa(innerSlot.HydrationID) + `--><p vela-parent="" class="snapshot-inner-slot" title="server" style="color:red"><!--v:vela-parent:` + itoa(innerSlotText.DynamicID) + `-->server<!--/v:vela-parent:` + itoa(innerSlotText.DynamicID) + `--></p><!--/e:vela-parent:` + itoa(innerSlot.HydrationID) + `--><!--e:vela-leaf:` + itoa(leafRoot.HydrationID) + `--><button class="snapshot-leaf"><!--v:vela-leaf:` + itoa(leafText.DynamicID) + `-->server<!--/v:vela-leaf:` + itoa(leafText.DynamicID) + `--></button><!--/e:vela-leaf:` + itoa(leafRoot.HydrationID) + `--><script type="application/json" id="vela-state-vela-leaf">{"component":"vela-leaf","props":{"Title":"server"},"state":{},"incomplete":false}</script><!--v:vela-parent:` + itoa(innerSlotIf.DynamicID) + `-->Value: <!--v:vela-parent:` + itoa(innerSlotIfText.DynamicID) + `-->server<!--/v:vela-parent:` + itoa(innerSlotIfText.DynamicID) + `--><!--/v:vela-parent:` + itoa(innerSlotIf.DynamicID) + `--><!--v:vela-parent:` + itoa(innerSlotElementIf.DynamicID) + `--><!--e:vela-parent:` + itoa(innerSlotElement.HydrationID) + `--><p vela-parent="" class="snapshot-inner-branch" title="server" style="color:red">Branch</p><!--/e:vela-parent:` + itoa(innerSlotElement.HydrationID) + `--><!--/v:vela-parent:` + itoa(innerSlotElementIf.DynamicID) + `--><!--e:vela-parent:` + itoa(innerSlotInput.HydrationID) + `--><input vela-parent="" class="snapshot-inner-slot-value" value="server"><!--/e:vela-parent:` + itoa(innerSlotInput.HydrationID) + `--><!--e:vela-parent:` + itoa(innerSlotEnabled.HydrationID) + `--><input vela-parent="" class="snapshot-inner-slot-enabled" type="checkbox"><!--/e:vela-parent:` + itoa(innerSlotEnabled.HydrationID) + `--><button class="snapshot-inner-action">Action</button>`
	html = strings.Replace(html,
		`<!--/e:vela-inner:`+itoa(innerButton.HydrationID)+`--></section>`,
		`<!--/e:vela-inner:`+itoa(innerButton.HydrationID)+`-->`+innerStaticSlots+`</section>`,
		1,
	)
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Outer.js":                 []byte(PrintJS(outerCSR, PrintPretty)),
		"Outer.hydrate_client.js":  []byte(PrintJS(outerClient, PrintPretty)),
		"Outer.hydrate.js":         []byte(PrintJS(outerEntry, PrintPretty)),
		"Inner.js":                 []byte(PrintJS(innerCSR, PrintPretty)),
		"Inner.hydrate_client.js":  []byte(PrintJS(innerClient, PrintPretty)),
		"Inner.hydrate.js":         []byte(PrintJS(innerEntry, PrintPretty)),
		"Leaf.js":                  []byte(PrintJS(leafCSR, PrintPretty)),
		"Leaf.hydrate_client.js":   []byte(PrintJS(leafClient, PrintPretty)),
		"Leaf.hydrate.js":          []byte(PrintJS(leafEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("projected child hydration mutated DOM");await p.evaluate(()=>{window.inner=document.querySelector(".snapshot-inner");window.input=document.querySelector(".snapshot-inner-value");window.close=document.querySelector(".snapshot-inner-close");window.input.value="bound";window.input.dispatchEvent(new Event("input",{bubbles:true}));window.close.click();window.inner.click();window.hydrated.component.set({title:"client"})});await p.waitForFunction(()=>window.snapshotBound==="bound"&&window.snapshotCloses===1&&window.snapshotInnerClicks===1&&document.querySelector(".snapshot-inner")?.textContent==="client");if(!(await p.evaluate(()=>document.querySelector(".snapshot-inner")===window.inner)))throw new Error("projected child was remounted for prop update");await p.evaluate(()=>{window.nextRequest=new Promise(resolve=>{window.resolveNext=resolve});window.hydrated.component.set({request:window.nextRequest})});await p.waitForFunction(()=>document.querySelector(".pending"));const removed=await p.evaluate(()=>{window.input.value="stale";window.input.dispatchEvent(new Event("input",{bubbles:true}));window.close.click();window.inner.click();return {detached:!window.inner.isConnected,destroys:window.snapshotInnerDestroys,payload:!document.getElementById("vela-state-vela-inner"),clicks:window.snapshotInnerClicks,closes:window.snapshotCloses}});if(!removed.detached||!removed.payload||removed.destroys!==1||removed.clicks!==1||removed.closes!==1)throw new Error(JSON.stringify(removed));await p.evaluate(()=>window.resolveNext("again"));await p.waitForFunction(()=>document.querySelector(".snapshot-inner")?.textContent==="client"&&document.querySelector(".snapshot-inner-value")?.value==="bound");await p.evaluate(()=>{window.remountedInput=document.querySelector(".snapshot-inner-value");window.remountedInput.value="again";window.remountedInput.dispatchEvent(new Event("input",{bubbles:true}));document.querySelector(".snapshot-inner-close").click();document.querySelector(".snapshot-inner").click()});await p.waitForFunction(()=>window.snapshotBound==="again"&&window.snapshotCloses===2&&window.snapshotInnerClicks===2);await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>window.snapshotInnerDestroys)!==2)throw new Error("remounted projected child was not destroyed");if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	browser = strings.ReplaceAll(browser,
		`window.hydrated.component.set({title:"client"})`,
		`window.hydrated.component.set({title:"client",active:true,color:"blue",draft:"external",enabled:false})`,
	)
	browser = strings.ReplaceAll(browser,
		`window.snapshotCloses=0;`,
		`window.snapshotCloses=0;window.snapshotLeafDestroys=0;window.snapshotSlotClicks=0;`,
	)
	browser = strings.ReplaceAll(browser,
		`window.close=document.querySelector(".snapshot-inner-close");window.input.value`,
		`window.close=document.querySelector(".snapshot-inner-close");window.leaf=document.querySelector(".snapshot-leaf");window.slot=document.querySelector(".snapshot-inner-slot");window.slotInput=document.querySelector(".snapshot-inner-slot-value");window.slotEnabled=document.querySelector(".snapshot-inner-slot-enabled");window.slotInput.value="typed-before-update";window.slotInput.dispatchEvent(new Event("input",{bubbles:true}));window.slotEnabled.checked=true;window.slotEnabled.dispatchEvent(new Event("change",{bubbles:true}));window.slot.click();window.input.value`,
	)
	browser = strings.ReplaceAll(browser,
		`window.snapshotInnerClicks===1&&document.querySelector(".snapshot-inner")?.textContent===`,
		`window.snapshotSlotClicks===1&&window.snapshotSlotDraft==="typed-before-update"&&window.snapshotSlotEnabled===true&&window.snapshotInnerClicks===1&&document.querySelector(".snapshot-inner-slot-value")?.value==="external"&&!document.querySelector(".snapshot-inner-slot-enabled")?.checked&&document.querySelector(".snapshot-leaf")?.textContent==="client"&&document.querySelector(".snapshot-inner-wrap")?.textContent.includes("Value: client")&&document.querySelector(".snapshot-inner-branch")?.title==="client"&&document.querySelector(".snapshot-inner-branch")?.classList.contains("active")&&document.querySelector(".snapshot-inner-branch")?.style.color==="blue"&&window.inner?.textContent===`,
	)
	browser = strings.ReplaceAll(browser,
		`if(!(await p.evaluate(()=>document.querySelector(".snapshot-inner")===window.inner)))throw new Error("projected child was remounted for prop update")`,
		`if(!(await p.evaluate(()=>document.querySelector(".snapshot-inner")===window.inner&&document.querySelector(".snapshot-leaf")===window.leaf)))throw new Error("projected child or nested leaf was remounted for prop update");await p.evaluate(()=>window.hydrated.component.set({show:false,elementShown:false}));await p.waitForFunction(()=>document.querySelector(".snapshot-inner-wrap")?.textContent.includes("Off")&&document.querySelector(".snapshot-inner-branch-off")?.textContent==="Branch off")`,
	)
	browser = strings.ReplaceAll(browser,
		`window.close.click();window.inner.click();return`,
		`window.close.click();window.inner.click();window.slotInput.value="stale";window.slotInput.dispatchEvent(new Event("input",{bubbles:true}));window.slotEnabled.checked=true;window.slotEnabled.dispatchEvent(new Event("change",{bubbles:true}));window.slot.click();return`,
	)
	browser = strings.ReplaceAll(browser,
		`clicks:window.snapshotInnerClicks,closes:window.snapshotCloses`,
		`clicks:window.snapshotInnerClicks,slotClicks:window.snapshotSlotClicks,leaf:!window.leaf.isConnected,leafDestroys:window.snapshotLeafDestroys,leafInnerAlive:window.snapshotLeafInnerAlive,leafPayload:!document.getElementById("vela-state-vela-leaf"),closes:window.snapshotCloses`,
	)
	browser = strings.ReplaceAll(browser,
		`||removed.clicks!==1||removed.closes!==1`,
		`||removed.clicks!==1||removed.slotClicks!==1||!removed.leaf||removed.leafDestroys!==1||!removed.leafInnerAlive||!removed.leafPayload||removed.closes!==1`,
	)
	browser = strings.ReplaceAll(browser,
		`document.querySelector(".snapshot-inner-close").click();document.querySelector(".snapshot-inner").click()`,
		`document.querySelector(".snapshot-inner-close").click();document.querySelector(".snapshot-inner").click();window.remountedLeaf=document.querySelector(".snapshot-leaf");window.remountedSlot=document.querySelector(".snapshot-inner-slot");window.remountedSlotInput=document.querySelector(".snapshot-inner-slot-value");window.remountedSlotEnabled=document.querySelector(".snapshot-inner-slot-enabled");window.remountedSlotInput.value="fresh";window.remountedSlotInput.dispatchEvent(new Event("input",{bubbles:true}));window.remountedSlotEnabled.checked=true;window.remountedSlotEnabled.dispatchEvent(new Event("change",{bubbles:true}));window.remountedSlot.click()`,
	)
	browser = strings.ReplaceAll(browser,
		`window.snapshotBound==="again"&&window.snapshotCloses===2&&window.snapshotInnerClicks===2`,
		`window.snapshotBound==="again"&&window.snapshotCloses===2&&window.snapshotInnerClicks===2&&window.snapshotSlotClicks===2&&window.snapshotSlotDraft==="fresh"&&window.snapshotSlotEnabled===true`,
	)
	browser = strings.ReplaceAll(browser,
		`await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>window.snapshotInnerDestroys)!==2)`,
		`await p.evaluate(()=>{if(window.remountedLeaf===window.leaf)throw new Error("nested leaf reused detached DOM instead of CSR remount");window.hydrated.component.destroy();window.remountedSlotInput.value="stale-after-destroy";window.remountedSlotInput.dispatchEvent(new Event("input",{bubbles:true}));window.remountedSlotEnabled.checked=false;window.remountedSlotEnabled.dispatchEvent(new Event("change",{bubbles:true}));window.remountedSlot.click();if(window.snapshotSlotClicks!==2)throw new Error("destroyed slot event still fired")});if(await p.evaluate(()=>window.snapshotInnerDestroys!==2||window.snapshotLeafDestroys!==2))`,
	)
	browser = strings.ReplaceAll(browser,
		`document.querySelector(".snapshot-inner")?.textContent==="client"`,
		`document.querySelector(".snapshot-inner")?.textContent==="client"&&document.querySelector(".snapshot-inner-slot")?.textContent==="client"&&document.querySelector(".snapshot-inner-slot")?.title==="client"&&document.querySelector(".snapshot-inner-slot")?.classList.contains("active")&&document.querySelector(".snapshot-inner-slot")?.style.color==="blue"&&document.querySelector(".snapshot-inner-slot-value")?.value==="external"&&!document.querySelector(".snapshot-inner-slot-enabled")?.checked&&document.querySelector(".snapshot-leaf")?.textContent==="client"&&document.querySelector(".snapshot-inner-wrap")?.textContent.includes("Off")&&document.querySelector(".snapshot-inner-branch-off")?.textContent==="Branch off"`,
	)
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("await snapshot projected child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("await snapshot projected child browser output=%q", got)
	}
}

func TestSSRHydrationStaticAwaitSnapshotKeepsSettledRange(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request;</script><template><main>{#await request}<p>Pending</p>{:then value}<p>Settled</p>{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(client, PrintPretty)
	for _, want := range []string{
		"const $$hawait0_snapshot = $$props.$$velaAwait && $$props.$$velaAwait[0];",
		"$$hawait0_snapshot.state === 'then'",
		"let $$hawait0_initial = true;",
		"if ($$hawait0_initial)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration await snapshot lowering misses %q:\n%s", want, source)
		}
	}
}

func TestBrowserSSRHydrationAdoptsStaticAwaitSnapshot(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request;</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<p class="settled">Settled</p>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	settled := await.ThenChildren[0].(*SSRPlanElement)
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--e:vela-page:` + itoa(settled.HydrationID) + `--><p class="settled">Settled</p><!--/e:vela-page:` + itoa(settled.HydrationID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.resolveRequest=null;window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
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
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("settled snapshot hydration mutated DOM");await p.evaluate(()=>{window.initialNode=document.querySelector(".settled");window.resolveRequest("same")});await p.waitForFunction(()=>document.querySelector(".settled")===window.initialNode);await p.evaluate(()=>window.hydrated.component.set({request:Promise.reject("failed")}));await p.waitForFunction(()=>document.querySelector(".failed"));const state=await p.evaluate(()=>({settledDetached:!window.initialNode.isConnected,pending:!!document.querySelector(".pending")}));if(!state.settledDetached||state.pending)throw new Error(JSON.stringify(state));await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("static await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("static await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsDynamicAwaitSnapshotLeaves(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let draft = "server"; export let enabled = false; function clicked() { window.snapshotClicks++; } function report() { window.snapshotDraft = draft; window.snapshotEnabled = enabled; }</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<p class="settled" :title={value}>Value: {value}</p><input class="snapshot-draft" bind:value={draft} /><input class="snapshot-enabled" type="checkbox" bind:checked={enabled} /><button class="snapshot-action" @click={clicked}>Increment</button><button class="snapshot-report" @click={report}>Report</button>{:catch error}<p class="failed">Failed: {error}</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	settled := await.ThenChildren[0].(*SSRPlanElement)
	text := settled.Children[0].(*SSRPlanText)
	textID := text.Parts[1].DynamicID
	draft := await.ThenChildren[1].(*SSRPlanElement)
	enabled := await.ThenChildren[2].(*SSRPlanElement)
	action := await.ThenChildren[3].(*SSRPlanElement)
	report := await.ThenChildren[4].(*SSRPlanElement)
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--e:vela-page:` + itoa(settled.HydrationID) + `--><p class="settled" title="server">Value: <!--v:vela-page:` + itoa(textID) + `-->server<!--/v:vela-page:` + itoa(textID) + `--></p><!--/e:vela-page:` + itoa(settled.HydrationID) + `--><!--e:vela-page:` + itoa(draft.HydrationID) + `--><input class="snapshot-draft" value="server"><!--/e:vela-page:` + itoa(draft.HydrationID) + `--><!--e:vela-page:` + itoa(enabled.HydrationID) + `--><input class="snapshot-enabled" type="checkbox"><!--/e:vela-page:` + itoa(enabled.HydrationID) + `--><!--e:vela-page:` + itoa(action.HydrationID) + `--><button class="snapshot-action" data-vela-event="vela-page-e0">Increment</button><!--/e:vela-page:` + itoa(action.HydrationID) + `--><!--e:vela-page:` + itoa(report.HydrationID) + `--><button class="snapshot-report" data-vela-event="vela-page-e1">Report</button><!--/e:vela-page:` + itoa(report.HydrationID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.snapshotClicks=0;window.request=new Promise(resolve=>{window.resolveRequest=resolve});document.querySelector(".snapshot-draft").value="typed-before-hydration";document.querySelector(".snapshot-enabled").checked=true;window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request,draft:"server",enabled:false});window.initial=window.observer.takeRecords().length</script></body>`
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
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("dynamic settled snapshot hydration mutated DOM");await p.evaluate(()=>{window.settled=document.querySelector(".settled");window.snapshotButton=document.querySelector(".snapshot-action");window.snapshotDraftInput=document.querySelector(".snapshot-draft");window.snapshotEnabledInput=document.querySelector(".snapshot-enabled");document.querySelector(".snapshot-report").click()});await p.waitForFunction(()=>window.snapshotDraft==="typed-before-hydration"&&window.snapshotEnabled===true);await p.evaluate(()=>window.snapshotButton.click());await p.waitForFunction(()=>window.snapshotClicks===1);await p.evaluate(()=>window.resolveRequest("client"));await p.waitForFunction(()=>document.querySelector(".settled")?.textContent==="Value: client"&&document.querySelector(".settled")?.getAttribute("title")==="client");await p.evaluate(()=>window.hydrated.component.set({draft:"external",enabled:false}));await p.waitForFunction(()=>document.querySelector(".snapshot-draft")?.value==="external"&&!document.querySelector(".snapshot-enabled")?.checked);const state=await p.evaluate(()=>({sameElement:document.querySelector(".settled")===window.settled,pending:!!document.querySelector(".pending")}));if(!state.sameElement||state.pending)throw new Error(JSON.stringify(state));await p.evaluate(()=>{window.nextRequest=new Promise(resolve=>{window.resolveNext=resolve});window.hydrated.component.set({request:window.nextRequest})});await p.waitForFunction(()=>document.querySelector(".pending"));await p.evaluate(()=>{window.snapshotDraftInput.value="stale";window.snapshotDraftInput.dispatchEvent(new Event("input",{bubbles:true}));window.snapshotEnabledInput.checked=true;window.snapshotEnabledInput.dispatchEvent(new Event("change",{bubbles:true}))});await p.evaluate(()=>window.resolveNext("again"));await p.waitForFunction(()=>document.querySelector(".snapshot-report"));await p.evaluate(()=>document.querySelector(".snapshot-report").click());await p.waitForFunction(()=>window.snapshotDraft==="external"&&window.snapshotEnabled===false);await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic await snapshot leaf browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic await snapshot leaf browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsAwaitSnapshotChild(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.snapshotChildDestroys = (window.snapshotChildDestroys || 0) + 1; });</script><template><button class="snapshot-child" @click={() => $$dispatch("close")}>{title}</button></template>`)
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

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; function record() { window.snapshotChildEvents = (window.snapshotChildEvents || 0) + 1; }</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<Child :title={value} @on:close={record} /><p class="between">Between</p><Child :title={value} @on:close={record} /><p class="settled">Done</p>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", parentSFC, parentAnalysis, "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", SourcePath: "Child.vela", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
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
	awaitNode := main.Children[0].(*AwaitBlockNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(awaitNode.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	childRoot := childPlan.Nodes[0].(*SSRPlanElement)
	childText := childRoot.Children[0].(*SSRPlanText).Parts[0]
	thenChildren := awaitNode.ThenChildren
	firstChild := thenChildren[0].(*ComponentNode)
	secondChild := thenChildren[2].(*ComponentNode)
	childScope := func(component *ComponentNode) string {
		suffix := parentPlan.ComponentInstanceSuffixes[component.Span]
		if suffix == "" {
			t.Fatalf("snapshot child chain is missing static instance suffix")
		}
		return parentPlan.ScopeID + "--" + suffix
	}
	childHTML := func(scope string) string {
		return `<!--e:` + scope + `:` + itoa(childRoot.HydrationID) + `--><button vela-child="" class="snapshot-child" data-vela-event="` + scope + `-e0"><!--v:` + scope + `:` + itoa(childText.DynamicID) + `-->server<!--/v:` + scope + `:` + itoa(childText.DynamicID) + `--></button><!--/e:` + scope + `:` + itoa(childRoot.HydrationID) + `--><script type="application/json" id="vela-state-` + scope + `">{"component":"` + scope + `","props":{"Title":"server"},"state":{},"incomplete":false}</script>`
	}
	firstScope := childScope(firstChild)
	secondScope := childScope(secondChild)
	html := `<!doctype html><body><!--e:vela-parent:` + itoa(parentPlan.Nodes[0].(*SSRPlanElement).HydrationID) + `--><main vela-parent=""><!--v:vela-parent:` + itoa(awaitID) + `-->` + childHTML(firstScope) + `<p vela-parent="" class="between">Between</p>` + childHTML(secondScope) + `<p vela-parent="" class="settled">Done</p><!--/v:vela-parent:` + itoa(awaitID) + `--></main><!--/e:vela-parent:` + itoa(parentPlan.Nodes[0].(*SSRPlanElement).HydrationID) + `--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{},"state":{},"incomplete":false,"await":{"` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.snapshotChildEvents=0;window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydrateParent(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],scopes=process.argv.slice(2),server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("snapshot child hydration mutated DOM");await p.evaluate(()=>{window.children=[...document.querySelectorAll(".snapshot-child")];if(window.children.length!==2)throw new Error("expected two adopted snapshot children");window.children.forEach(child=>child.click())});await p.waitForFunction(()=>window.snapshotChildEvents===2);await p.evaluate(()=>window.resolveRequest("client"));await p.waitForFunction(()=>[...document.querySelectorAll(".snapshot-child")].length===2&&[...document.querySelectorAll(".snapshot-child")].every(child=>child.textContent==="client"));const same=await p.evaluate(()=>{const chain=[...document.querySelectorAll("main > .snapshot-child, main > .between")];return [...document.querySelectorAll(".snapshot-child")].every((child,index)=>child===window.children[index])&&chain[0]===window.children[0]&&chain[1]===document.querySelector(".between")&&chain[2]===window.children[1]});if(!same)throw new Error("snapshot child chain was remounted or reordered on same-branch settle");await p.evaluate(()=>window.children.forEach(child=>child.click()));await p.waitForFunction(()=>window.snapshotChildEvents===4);await p.evaluate(()=>{window.nextRequest=new Promise(()=>{});window.hydrated.component.set({request:window.nextRequest})});await p.waitForFunction(()=>document.querySelector(".pending"));await p.evaluate(()=>window.children.forEach(child=>child.click()));if(await p.evaluate(()=>window.snapshotChildEvents)!==4)throw new Error("detached snapshot child listener remained active");const state=await p.evaluate(scopes=>({detached:window.children.every(child=>!child.isConnected),payloads:scopes.every(scope=>!document.getElementById("vela-state-"+scope)),destroys:window.snapshotChildDestroys}),scopes);if(!state.detached||!state.payloads||state.destroys!==2)throw new Error(JSON.stringify(state));await p.evaluate(()=>window.hydrated.component.destroy());process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir, firstScope, secondScope)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("await snapshot child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("await snapshot child browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsAwaitSnapshotBindableChild(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let value = $bindable("");</script><template><input class="snapshot-bindable" bind:value={value} /></template>`)
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

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; let draft = $state("server");</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<Child bind:value={draft} /><p class="settled">Done</p>{:catch error}<p class="failed">Failed</p>{/await}<p class="parent-value">{draft}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", parentSFC, parentAnalysis, "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", SourcePath: "Child.vela", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
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
	parentRoot := parentPlan.Nodes[0].(*SSRPlanElement)
	awaitNode := parentSFC.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*AwaitBlockNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(awaitNode.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	valueElement := parentRoot.Children[1].(*SSRPlanElement)
	valueText := valueElement.Children[0].(*SSRPlanText).Parts[0]
	childRoot := childPlan.Nodes[0].(*SSRPlanElement)
	childScope := childPlan.ScopeID
	html := `<!doctype html><body><!--e:vela-parent:` + itoa(parentRoot.HydrationID) + `--><main vela-parent=""><!--v:vela-parent:` + itoa(awaitID) + `--><!--e:` + childScope + `:` + itoa(childRoot.HydrationID) + `--><input vela-child="" class="snapshot-bindable" value="server"><!--/e:` + childScope + `:` + itoa(childRoot.HydrationID) + `--><script type="application/json" id="vela-state-` + childScope + `">{"component":"` + childScope + `","props":{"Value":"server"},"state":{},"incomplete":false}</script><p vela-parent="" class="settled">Done</p><!--/v:vela-parent:` + itoa(awaitID) + `--><!--e:vela-parent:` + itoa(valueElement.HydrationID) + `--><p vela-parent="" class="parent-value"><!--v:vela-parent:` + itoa(valueText.DynamicID) + `-->server<!--/v:vela-parent:` + itoa(valueText.DynamicID) + `--></p><!--/e:vela-parent:` + itoa(valueElement.HydrationID) + `--></main><!--/e:vela-parent:` + itoa(parentRoot.HydrationID) + `--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{},"state":{"draft":"server"},"incomplete":false,"await":{"` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydrateParent(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],scope=process.argv[2],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("bindable snapshot hydration mutated DOM");await p.evaluate(()=>{window.input=document.querySelector(".snapshot-bindable")});await p.locator(".snapshot-bindable").fill("client");await p.waitForFunction(()=>document.querySelector(".parent-value")?.textContent==="client");await p.evaluate(()=>window.resolveRequest("same"));await p.waitForFunction(()=>document.querySelector(".snapshot-bindable")===window.input&&document.querySelector(".snapshot-bindable")?.value==="client");await p.evaluate(()=>window.hydrated.component.set({draft:"external"}));await p.waitForFunction(()=>document.querySelector(".snapshot-bindable")?.value==="external"&&document.querySelector(".parent-value")?.textContent==="external");await p.evaluate(()=>{window.nextRequest=new Promise(()=>{});window.hydrated.component.set({request:window.nextRequest})});await p.waitForFunction(()=>document.querySelector(".pending"));await p.evaluate(async()=>{window.input.value="stale";window.input.dispatchEvent(new Event("input",{bubbles:true}));await new Promise(resolve=>setTimeout(resolve,0));window.afterDetached=document.querySelector(".parent-value").textContent});if(await p.evaluate(()=>window.afterDetached)!=="external")throw new Error("detached bindable child still updated parent");const state=await p.evaluate(scope=>({detached:!window.input.isConnected,payload:!document.getElementById("vela-state-"+scope)}),scope);if(!state.detached||!state.payload)throw new Error(JSON.stringify(state));await p.evaluate(()=>window.hydrated.component.destroy());process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir, childScope)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("await snapshot bindable child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("await snapshot bindable child browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsAwaitSnapshotReactiveSlotLeafChild(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<template><section class="snapshot-slot-child"><slot><p class="fallback">Fallback</p></slot></section></template>`)
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

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let title = "server"; export let active = false; export let color = "red"; export let draft = "server"; export let enabled = false; function increment() { window.slotClicks = (window.slotClicks || 0) + 1; window.slotDraft = draft; window.slotEnabled = enabled; }</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<Child><p class="snapshot-slot" :title={title} class:active={active} style:color={color}>{title}</p><p class="snapshot-value" :data-value={value}>{value}</p><button class="snapshot-event" @click={increment}>Increment</button><input class="snapshot-draft" bind:value={draft} /><input class="snapshot-enabled" type="checkbox" bind:checked={enabled} /></Child><p class="settled">Done</p>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", parentSFC, parentAnalysis, "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", SourcePath: "Child.vela", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
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
	parentRoot := parentPlan.Nodes[0].(*SSRPlanElement)
	awaitPlan := parentRoot.Children[0].(*SSRPlanAwait)
	awaitNode := parentSFC.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*AwaitBlockNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(awaitNode.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	componentPlan := awaitPlan.ThenChildren[0].(*SSRPlanComponent)
	slotElement := componentPlan.Slots["default"][0].(*SSRPlanElement)
	slotText := slotElement.Children[0].(*SSRPlanText).Parts[0]
	slotValueElement := componentPlan.Slots["default"][1].(*SSRPlanElement)
	slotValueText := slotValueElement.Children[0].(*SSRPlanText).Parts[0]
	slotEventElement := componentPlan.Slots["default"][2].(*SSRPlanElement)
	slotDraftElement := componentPlan.Slots["default"][3].(*SSRPlanElement)
	slotEnabledElement := componentPlan.Slots["default"][4].(*SSRPlanElement)
	childRoot := childPlan.Nodes[0].(*SSRPlanElement)
	childScope := childPlan.ScopeID
	html := `<!doctype html><body><!--e:vela-parent:` + itoa(parentRoot.HydrationID) + `--><main vela-parent=""><!--v:vela-parent:` + itoa(awaitID) + `--><!--e:` + childScope + `:` + itoa(childRoot.HydrationID) + `--><section vela-child="" class="snapshot-slot-child"><!--e:vela-parent:` + itoa(slotElement.HydrationID) + `--><p vela-parent="" class="snapshot-slot" title="server" style="color:red"><!--v:vela-parent:` + itoa(slotText.DynamicID) + `-->server<!--/v:vela-parent:` + itoa(slotText.DynamicID) + `--></p><!--/e:vela-parent:` + itoa(slotElement.HydrationID) + `--><!--e:vela-parent:` + itoa(slotValueElement.HydrationID) + `--><p vela-parent="" class="snapshot-value" data-value="server"><!--v:vela-parent:` + itoa(slotValueText.DynamicID) + `-->server<!--/v:vela-parent:` + itoa(slotValueText.DynamicID) + `--></p><!--/e:vela-parent:` + itoa(slotValueElement.HydrationID) + `--><!--e:vela-parent:` + itoa(slotEventElement.HydrationID) + `--><button vela-parent="" class="snapshot-event">Increment</button><!--/e:vela-parent:` + itoa(slotEventElement.HydrationID) + `--><!--e:vela-parent:` + itoa(slotDraftElement.HydrationID) + `--><input vela-parent="" class="snapshot-draft" value="server"><!--/e:vela-parent:` + itoa(slotDraftElement.HydrationID) + `--><!--e:vela-parent:` + itoa(slotEnabledElement.HydrationID) + `--><input vela-parent="" class="snapshot-enabled" type="checkbox"><!--/e:vela-parent:` + itoa(slotEnabledElement.HydrationID) + `--></section><!--/e:` + childScope + `:` + itoa(childRoot.HydrationID) + `--><script type="application/json" id="vela-state-` + childScope + `">{"component":"` + childScope + `","props":{},"state":{},"incomplete":false}</script><p vela-parent="" class="settled">Done</p><!--/v:vela-parent:` + itoa(awaitID) + `--></main><!--/e:vela-parent:` + itoa(parentRoot.HydrationID) + `--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Title":"server","Active":false,"Color":"red","Draft":"server","Enabled":false},"state":{},"incomplete":false,"await":{"` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.slotClicks=0;window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydrateParent(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],scope=process.argv[2],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("snapshot slot hydration mutated DOM");await p.evaluate(()=>{window.child=document.querySelector(".snapshot-slot-child");window.slot=document.querySelector(".snapshot-slot");window.valueSlot=document.querySelector(".snapshot-value");window.eventSlot=document.querySelector(".snapshot-event");window.draftSlot=document.querySelector(".snapshot-draft");window.enabledSlot=document.querySelector(".snapshot-enabled");window.draftSlot.value="typed-before-hydration";window.draftSlot.dispatchEvent(new Event("input",{bubbles:true}));window.enabledSlot.checked=true;window.enabledSlot.dispatchEvent(new Event("change",{bubbles:true}));window.eventSlot.click();window.hydrated.component.set({title:"client",active:true,color:"blue"})});await p.waitForFunction(()=>window.slotClicks===1&&window.slotDraft==="typed-before-hydration"&&window.slotEnabled===true&&document.querySelector(".snapshot-slot")?.textContent==="client"&&document.querySelector(".snapshot-slot")?.title==="client"&&document.querySelector(".snapshot-slot")?.classList.contains("active")&&document.querySelector(".snapshot-slot")?.style.color==="blue");if(!(await p.evaluate(()=>document.querySelector(".snapshot-slot")===window.slot)))throw new Error("reactive snapshot slot was replaced");await p.evaluate(()=>window.resolveRequest("settled"));await p.waitForFunction(()=>document.querySelector(".snapshot-value")?.textContent==="settled"&&document.querySelector(".snapshot-value")?.dataset.value==="settled");if(!(await p.evaluate(()=>document.querySelector(".snapshot-value")===window.valueSlot)))throw new Error("same-branch snapshot slot value was replaced");await p.evaluate(()=>window.hydrated.component.set({draft:"external",enabled:false}));await p.waitForFunction(()=>document.querySelector(".snapshot-draft")?.value==="external"&&!document.querySelector(".snapshot-enabled")?.checked);await p.evaluate(()=>{window.nextRequest=new Promise(resolve=>{window.resolveNext=resolve});window.hydrated.component.set({request:window.nextRequest})});await p.waitForFunction(()=>document.querySelector(".pending"));const removed=await p.evaluate(scope=>{window.draftSlot.value="stale";window.draftSlot.dispatchEvent(new Event("input",{bubbles:true}));window.enabledSlot.checked=true;window.enabledSlot.dispatchEvent(new Event("change",{bubbles:true}));window.eventSlot.click();return {child:!window.child.isConnected,slot:!window.slot.isConnected,value:!window.valueSlot.isConnected,event:!window.eventSlot.isConnected,payload:!document.getElementById("vela-state-"+scope),clicks:window.slotClicks}},scope);if(!removed.child||!removed.slot||!removed.value||!removed.event||!removed.payload||removed.clicks!==1)throw new Error(JSON.stringify(removed));await p.evaluate(()=>window.resolveNext("again"));await p.waitForFunction(()=>document.querySelector(".snapshot-slot-child")&&document.querySelector(".snapshot-slot")?.textContent==="client"&&document.querySelector(".snapshot-slot")?.title==="client"&&document.querySelector(".snapshot-slot")?.classList.contains("active")&&document.querySelector(".snapshot-slot")?.style.color==="blue"&&document.querySelector(".snapshot-value")?.textContent==="again"&&document.querySelector(".snapshot-value")?.dataset.value==="again"&&document.querySelector(".snapshot-draft")?.value==="external"&&!document.querySelector(".snapshot-enabled")?.checked);await p.evaluate(()=>{window.remountedEvent=document.querySelector(".snapshot-event");window.remountedEvent.click()});await p.waitForFunction(()=>window.slotClicks===2&&window.slotDraft==="external"&&window.slotEnabled===false);const remounted=await p.evaluate(()=>({sameChild:document.querySelector(".snapshot-slot-child")===window.child,sameSlot:document.querySelector(".snapshot-slot")===window.slot,sameValue:document.querySelector(".snapshot-value")===window.valueSlot}));if(remounted.sameChild||remounted.sameSlot||remounted.sameValue)throw new Error("snapshot slot child reused detached DOM instead of CSR remount");await p.evaluate(()=>{window.hydrated.component.destroy();window.remountedEvent.click();if(window.slotClicks!==2)throw new Error("destroyed slot event still fired")});if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e,errors.join("\\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir, childScope)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("await snapshot static-slot child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("await snapshot static-slot child browser output=%q", got)
	}
}
