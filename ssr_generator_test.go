package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// Generative differential test (docs/HYDRATION_ARCHITECTURE.md Q-10.2,
// task 041). A seeded generator builds components from every block kind,
// element features, a child component with slot content, events, and
// state initialized from props, together with random data and a random
// sequence of updates. Each case runs through the SSR block suite: Go SSR,
// hydrate(), equality with the client DOM, no DOM mutation, node identity,
// equality after every update, and destroy(). A failure names its seed;
// add the generated component and case to testdata/ssr_blocks to keep it.

const ssrGenChild = `<script>
  export let label = '';
  let c = $state(0);
</script>
<template>
  <section class="child"><h3>{label}</h3><button class="cb" @click={() => c++}>{c}</button><div class="slot"><slot>fallback</slot></div></section>
</template>
`

type ssrGenerator struct {
	r       *rand.Rand
	buttons int
}

var (
	ssrGenExprs = []string{"{title}", "{n}", "{n + 1}", "{doubled}", "{title + '!'}", "{open ? 'yes' : 'no'}", "{items.length}", "{count * 2}", "{flag}"}
	ssrGenRow   = []string{"{item.label}", "{item.on ? 'on' : 'off'}", "{index}", "{item.id + n}"}
	ssrGenConds = []string{"open", "n > 2", "items.length === 0", "title === 'hello'", "flag && n > 0", "doubled >= 4"}
	ssrGenRowC  = []string{"item.on", "index === 0", "item.id > 3"}
)

func (g *ssrGenerator) pick(list []string) string { return list[g.r.Intn(len(list))] }

func (g *ssrGenerator) expr(inRow bool) string {
	if inRow && g.r.Intn(2) == 0 {
		return g.pick(ssrGenRow)
	}
	return g.pick(ssrGenExprs)
}

func (g *ssrGenerator) cond(inRow bool) string {
	if inRow && g.r.Intn(2) == 0 {
		return g.pick(ssrGenRowC)
	}
	return g.pick(ssrGenConds)
}

func (g *ssrGenerator) button() string {
	g.buttons++
	action := "n = n + 1"
	if g.r.Intn(2) == 0 {
		action = "open = !open"
	}
	return fmt.Sprintf(`<button class="b%d" @click={() => %s}>+</button>`, g.buttons, action)
}

// nodes writes one to three siblings; depth bounds nesting and inRow says
// whether item and index are in scope.
func (g *ssrGenerator) nodes(depth int, inRow bool) string {
	var out strings.Builder
	count := 1 + g.r.Intn(3)
	for i := 0; i < count; i++ {
		kind := g.r.Intn(9)
		if depth >= 3 && kind >= 2 {
			kind = g.r.Intn(2)
		}
		switch kind {
		case 0:
			fmt.Fprintf(&out, "<span>%s</span>", g.expr(inRow))
		case 1:
			fmt.Fprintf(&out, "<b>t %s</b>", g.expr(inRow))
		case 2:
			fmt.Fprintf(&out, `<div class="d%d">%s</div>`, depth, g.nodes(depth+1, inRow))
		case 3:
			out.WriteString("{#if " + g.cond(inRow) + "}" + g.nodes(depth+1, inRow))
			if g.r.Intn(2) == 0 {
				out.WriteString("{:else if " + g.cond(inRow) + "}" + g.nodes(depth+1, inRow))
			}
			if g.r.Intn(2) == 0 {
				out.WriteString("{:else}" + g.nodes(depth+1, inRow))
			}
			out.WriteString("{/if}")
		case 4:
			if inRow {
				out.WriteString(g.button())
				continue
			}
			out.WriteString("<ul>{#each items as item, index (item.id)}<li>" + g.nodes(depth+1, true) + "</li>")
			if g.r.Intn(2) == 0 {
				out.WriteString(`{:else}<li class="none">none</li>`)
			}
			out.WriteString("{/each}</ul>")
		case 5:
			out.WriteString("{#key n}" + g.nodes(depth+1, inRow) + "{/key}")
		case 7:
			// A client boundary (contract §9.1): the server renders the
			// fallback, hydration mounts the main branch after the commit.
			out.WriteString("{#client}" + g.nodes(depth+1, inRow))
			if g.r.Intn(2) == 0 {
				out.WriteString("{:else}<i class=\"fallback\">" + g.expr(inRow) + "</i>")
			}
			out.WriteString("{/client}")
		case 6:
			label := "title"
			if inRow {
				label = "item.label"
			}
			out.WriteString("<Child :label={" + label + "}>" + g.nodes(depth+1, inRow) + "</Child>")
		default:
			out.WriteString(g.button())
		}
	}
	return out.String()
}

func (g *ssrGenerator) items() []map[string]any {
	ids := g.r.Perm(6)[:g.r.Intn(5)]
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, map[string]any{"id": id + 1, "label": g.pick([]string{"a", "bb", "<c>", "d & e"}), "on": g.r.Intn(2) == 0})
	}
	return items
}

func (g *ssrGenerator) props() map[string]any {
	return map[string]any{
		"items": g.items(), "title": g.pick([]string{"x", "hello", "<b>", "a & b"}),
		"count": g.r.Intn(5), "flag": g.r.Intn(2) == 0,
	}
}

func (g *ssrGenerator) steps() []map[string]any {
	var steps []map[string]any
	for i := 0; i < 4+g.r.Intn(3); i++ {
		switch g.r.Intn(4) {
		case 0:
			steps = append(steps, map[string]any{"set": map[string]any{"items": g.items()}})
		case 1:
			steps = append(steps, map[string]any{"set": map[string]any{"title": g.pick([]string{"x", "hello", "z"}), "flag": g.r.Intn(2) == 0}})
		case 2:
			if g.buttons > 0 {
				steps = append(steps, map[string]any{"click": fmt.Sprintf("button.b%d", 1+g.r.Intn(g.buttons))})
			}
		default:
			steps = append(steps, map[string]any{"click": "button.cb"})
		}
	}
	return steps
}

func ssrGenerateCase(seed int64) (name, source string, testCase ssrBlocksCase) {
	g := &ssrGenerator{r: rand.New(rand.NewSource(seed))}
	name = fmt.Sprintf("Gen%d", seed)
	body := g.nodes(0, false) + g.button()
	source = `<script>
  import Child from './Child.vela';
  export let items = [];
  export let title = 'x';
  export let count = 0;
  export let flag = false;
  let n = $state(count);
  let open = $state(flag);
  let doubled = $derived(n * 2);
</script>
<template>
  <div class="gen">` + body + `</div>
</template>
`
	props, _ := json.Marshal(g.props())
	var steps []json.RawMessage
	for _, step := range g.steps() {
		encoded, _ := json.Marshal(step)
		steps = append(steps, encoded)
	}
	testCase = ssrBlocksCase{Name: fmt.Sprintf("seed %d", seed), Component: name, Props: props, Steps: steps, Compare: true}
	if strings.Contains(source, "{#client}") {
		// The server writes fallbacks where the client writes main branches.
		testCase.Boundary = json.RawMessage(`{"fallback":["i.fallback"]}`)
	}
	return name, source, testCase
}

func TestBlocksGenerativeDifferential(t *testing.T) {
	files, cases := ssrGeneratedSuite()
	runSSRBlocksSuite(t, files, cases)
}

// TestBlocksGenerativeDifferentialInChromium runs the generated cases
// through hydration in Chromium as well (Q-10.2, task 052).
func TestBlocksGenerativeDifferentialInChromium(t *testing.T) {
	node, _ := requireJsdom(t)
	probe := `const fs=require("fs"); const {chromium}=require("playwright"); if (!fs.existsSync(chromium.executablePath())) process.exit(1)`
	if err := exec.Command(node, "-e", probe).Run(); err != nil {
		t.Skip("Playwright Chromium is unavailable; run npx playwright install chromium")
	}
	files, cases := ssrGeneratedSuite()
	runSSRBlocksSuite(t, files, cases, "--chromium")
}

// ssrGeneratedSuite generates the cases of VELA_GEN_SEEDS (default 30)
// seeds, or the one seed VELA_GEN_SEED.
func ssrGeneratedSuite() (map[string]string, []ssrBlocksCase) {
	first, count := int64(1), int64(30)
	if value, err := strconv.ParseInt(os.Getenv("VELA_GEN_SEEDS"), 10, 64); err == nil && value > 0 {
		count = value
	}
	if value, err := strconv.ParseInt(os.Getenv("VELA_GEN_SEED"), 10, 64); err == nil {
		first, count = value, 1
	}
	files := map[string]string{"Child.vela": ssrGenChild}
	var cases []ssrBlocksCase
	for seed := first; seed < first+count; seed++ {
		name, source, testCase := ssrGenerateCase(seed)
		files[name+".vela"] = source
		cases = append(cases, testCase)
	}
	return files, cases
}
