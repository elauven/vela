package main

import (
	"strings"
	"testing"
)

// Task 096: transition:, in:, out: and animate:. In jsdom element.animate
// is the harness stub: an animation runs until a finishAnimations step,
// __velaAnimations counts the started ones and __velaRunning the running.

func TestTransitionIfOutroWaitsAndIntroPlays(t *testing.T) {
	src := `<script>
  import { fade } from 'vela/transition';
  let show = $state(true);
</script>
<template><div>
  <button @click={() => show = !show}>toggle</button>
  {#if show}<p transition:fade={{ duration: 100 }}>x</p>{/if}
</div></template>`
	assertRuntime(t, "IfOutro", src, []rtStep{
		// No transition plays on the first render.
		{"assertCount": "p", "equals": 1},
		{"assertGlobal": "__velaAnimations", "equals": 0},
		// The outro keeps the paragraph until it finishes.
		{"click": "button"},
		{"assertCount": "p", "equals": 1},
		{"assertGlobal": "__velaRunning", "equals": 1},
		{"finishAnimations": true},
		{"assertCount": "p", "equals": 0},
		// The branch coming back plays its intro.
		{"click": "button"},
		{"assertCount": "p", "equals": 1},
		{"assertGlobal": "__velaAnimations", "equals": 2},
		{"finishAnimations": true},
		{"assertGlobal": "__velaRunning", "equals": 0},
		{"assertCount": "p", "equals": 1},
	})
}

func TestTransitionInterruptedLeavesNoNodes(t *testing.T) {
	src := `<script>
  import { fly } from 'vela/transition';
  let show = $state(false);
</script>
<template><div>
  <button @click={() => show = !show}>toggle</button>
  {#if show}<p transition:fly={{ y: 20 }}>x</p>{/if}
</div></template>`
	assertRuntime(t, "Interrupted", src, []rtStep{
		{"click": "button"},
		{"assertGlobal": "__velaRunning", "equals": 1},
		// Back out during the intro: the outro takes over the element.
		{"click": "button"},
		{"assertCount": "p", "equals": 1},
		{"assertGlobal": "__velaRunning", "equals": 1},
		// In again during the outro: the leaving one stays until its end.
		{"click": "button"},
		{"assertCount": "p", "equals": 2},
		// The component goes away mid-transition: nothing is left behind.
		{"remount": true},
		{"assertCount": "p", "equals": 0},
		{"assertGlobal": "__velaRunning", "equals": 0},
	})
}

func TestTransitionInAndOut(t *testing.T) {
	src := `<script>
  import { fade, fly } from 'vela/transition';
  let show = $state(true);
</script>
<template><div>
  <button @click={() => show = !show}>toggle</button>
  {#if show}<p in:fly={{ y: 10 }}>in only</p><b out:fade>out only</b>{/if}
</div></template>`
	assertRuntime(t, "InOut", src, []rtStep{
		// Only out: plays on removal, and holds the whole branch.
		{"click": "button"},
		{"assertGlobal": "__velaAnimations", "equals": 1},
		{"assertCount": "b", "equals": 1},
		{"finishAnimations": true},
		{"assertCount": "b", "equals": 0},
		{"assertCount": "p", "equals": 0},
		// Only in: plays on insertion.
		{"click": "button"},
		{"assertGlobal": "__velaAnimations", "equals": 2},
	})
}

func TestTransitionKeyedEachRows(t *testing.T) {
	src := `<script>
  import { fade } from 'vela/transition';
  let items = $state(['a', 'b', 'c']);
</script>
<template><div>
  <button class="drop" @click={() => items = items.filter((item) => item !== 'b')}>drop</button>
  <button class="add" @click={() => items = [...items, 'd']}>add</button>
  <button class="clear" @click={() => items = []}>clear</button>
  {#each items as item (item)}<li transition:fade>{item}</li>{:else}<em transition:fade>empty</em>{/each}
</div></template>`
	assertRuntime(t, "EachRows", src, []rtStep{
		{"assertCount": "li", "equals": 3},
		{"assertGlobal": "__velaAnimations", "equals": 0},
		{"click": "button.drop"},
		{"assertCount": "li", "equals": 3},
		{"finishAnimations": true},
		{"assertCount": "li", "equals": 2},
		{"click": "button.add"},
		{"assertGlobal": "__velaAnimations", "equals": 2},
		{"finishAnimations": true},
		{"assertText": "div", "equals": "drop add clear acd"},
		// Rows leave and the fallback comes in together.
		{"click": "button.clear"},
		{"assertCount": "li", "equals": 3},
		{"assertCount": "em", "equals": 1},
		{"assertGlobal": "__velaRunning", "equals": 4},
		{"finishAnimations": true},
		{"assertCount": "li", "equals": 0},
		{"assertCount": "em", "equals": 1},
	})
}

func TestTransitionIsLocal(t *testing.T) {
	child := `<script>
  import { fade } from 'vela/transition';
</script>
<template><aside transition:fade>child</aside></template>`
	parent := `<script>
  import { fade } from 'vela/transition';
  import { Child } from './Child.vela';
  let outer = $state(true);
  let inner = $state(true);
</script>
<template><div>
  <button class="outer" @click={() => outer = !outer}>outer</button>
  <button class="inner" @click={() => inner = !inner}>inner</button>
  {#if outer}<section>{#if inner}<p transition:fade>inner</p>{/if}<Child /></section>{/if}
</div></template>`
	assertRuntimeSet(t, "Parent", map[string]string{"Parent": parent, "Child": child}, []rtStep{
		// The outer block removes the section: the inner block's <p> does
		// not play (it is local to the inner {#if}); the root of the child
		// component belongs to the outer branch and plays.
		{"click": "button.outer"},
		{"assertGlobal": "__velaAnimations", "equals": 1},
		{"assertCount": "aside", "equals": 1},
		{"assertCount": "p", "equals": 1},
		{"finishAnimations": true},
		{"assertCount": "section", "equals": 0},
		// Coming back plays the child root's intro only.
		{"click": "button.outer"},
		{"assertGlobal": "__velaAnimations", "equals": 2},
		{"finishAnimations": true},
		// The inner block plays its own.
		{"click": "button.inner"},
		{"assertGlobal": "__velaAnimations", "equals": 3},
		{"assertCount": "p", "equals": 1},
		{"finishAnimations": true},
		{"assertCount": "p", "equals": 0},
	})
}

func TestTransitionReducedMotion(t *testing.T) {
	src := `<script>
  import { fade } from 'vela/transition';
  let show = $state(true);
</script>
<template><div>
  <button @click={() => show = !show}>toggle</button>
  {#if show}<p transition:fade>x</p>{/if}
</div></template>`
	assertRuntime(t, "Reduced", src, []rtStep{
		{"reducedMotion": true},
		{"click": "button"},
		{"assertCount": "p", "equals": 0},
		{"click": "button"},
		{"assertCount": "p", "equals": 1},
		{"assertGlobal": "__velaAnimations", "equals": 0},
	})
}

func TestTransitionCustomFunctionAndParams(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let distance = $state(5);
  function grow(node, params, options) {
    globalThis.__log = (globalThis.__log ? globalThis.__log + ' ' : '') + options.direction + ':' + params.by + ':' + node.localName;
    return { duration: 50, css: (t) => 'transform: scale(' + t + ')' };
  }
</script>
<template><div>
  <button class="toggle" @click={() => show = !show}>toggle</button>
  <button class="far" @click={() => distance = 9}>far</button>
  {#if show}<p transition:grow={{ by: distance }}>x</p>{/if}
</div></template>`
	assertRuntime(t, "Custom", src, []rtStep{
		{"click": "button.far"},
		{"click": "button.toggle"},
		{"assertGlobal": "__log", "equals": "out:9:p"},
		{"finishAnimations": true},
		{"click": "button.toggle"},
		{"assertGlobal": "__log", "equals": "out:9:p in:9:p"},
	})
}

func TestTransitionDiagnostics(t *testing.T) {
	cases := []struct {
		name, template, script, want string
	}{
		{"component", `<Child transition:fade />`, `import { Child } from './Child.vela'; import { fade } from 'vela/transition';`, "VELA-ACTION-TARGET"},
		{"modifier", `{#if a}<p transition:fade|global>x</p>{/if}`, `import { fade } from 'vela/transition'; let a = $state(true);`, "modifiers such as |global are not supported"},
		{"both", `{#if a}<p transition:fade in:fade>x</p>{/if}`, `import { fade } from 'vela/transition'; let a = $state(true);`, "VELA-TRANSITION-CONFLICT"},
		{"twice", `{#if a}<p in:fade in:fade>x</p>{/if}`, `import { fade } from 'vela/transition'; let a = $state(true);`, "more than one in:"},
		{"animate outside each", `<p animate:flip>x</p>`, `import { flip } from 'vela/animate';`, "VELA-ANIMATE-PLACEMENT"},
		{"animate in unkeyed each", `{#each xs as x}<p animate:flip>{x}</p>{/each}`, `import { flip } from 'vela/animate'; let xs = $state([1]);`, "VELA-ANIMATE-PLACEMENT"},
		{"animate with siblings", `{#each xs as x (x)}<p animate:flip>{x}</p><p>{x}</p>{/each}`, `import { flip } from 'vela/animate'; let xs = $state([1]);`, "VELA-ANIMATE-PLACEMENT"},
		{"quoted", `{#if a}<p transition:fade="x">x</p>{/if}`, `import { fade } from 'vela/transition'; let a = $state(true);`, "takes its parameters in braces"},
		{"unknown import", `<p>x</p>`, `import { wobble } from 'vela/transition';`, "VELA-UNKNOWN-IMPORT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := "<script>" + c.script + "</script>\n<template>" + c.template + "</template>\n"
			svc := &CompilerService{Cwd: mustGetwd(t)}
			res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source})
			var all []string
			for _, d := range res.Diagnostics {
				all = append(all, d.Code+": "+d.Message)
			}
			if !strings.Contains(strings.Join(all, "\n")+res.Error, c.want) {
				t.Fatalf("diagnostics %v (error %q) do not mention %q", all, res.Error, c.want)
			}
		})
	}
	// A valid animate: on the only element of a keyed row compiles.
	source := "<script>import { flip } from 'vela/animate'; let xs = $state([1]);</script>\n<template>{#each xs as x (x)}\n  <p animate:flip={{ duration: 100 }}>{x}</p>\n{/each}</template>\n"
	svc := &CompilerService{Cwd: mustGetwd(t)}
	if res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source}); res.Error != "" {
		t.Fatalf("animate: %s %v", res.Error, res.Diagnostics)
	}
}

// TestBrowserTransitions runs transitions with the Web Animations API of
// Chromium: an outro holds the element for its duration, animate:flip
// animates the rows that move, prefers-reduced-motion skips both.
func TestBrowserTransitions(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  import { fade, fly } from 'vela/transition';
  import { flip } from 'vela/animate';
  let show = $state(true);
  let items = $state([1, 2, 3, 4]);
  function probe() {
    globalThis.__running = document.getAnimations().filter((animation) => animation.playState === 'running').length;
  }
</script>
<template>
  <button class="toggle" @click={() => show = !show}>toggle</button>
  <button class="reverse" @click={() => items = [...items].reverse()}>reverse</button>
  <button class="probe" @click={probe}>probe</button>
  {#if show}<p class="note" transition:fade={{ duration: 300 }}>note</p>{/if}
  <ul>{#each items as item (item)}<li style="display: block; height: 20px" animate:flip={{ duration: 2000 }} in:fly={{ y: 20 }}>{item}</li>{/each}</ul>
</template>`}, "App.js", []rtStep{
		{"click": "button.toggle"},
		{"assertCount": "p.note", "equals": 1},
		{"click": "button.probe"},
		{"assertGlobal": "__running", "equals": 1},
		{"wait": 600},
		{"assertCount": "p.note", "equals": 0},
		{"click": "button.reverse"},
		{"click": "button.probe"},
		{"assertGlobal": "__running", "equals": 4},
		{"assertText": "ul", "equals": "4321"},
		{"finishAnimations": true},
		{"click": "button.probe"},
		{"assertGlobal": "__running", "equals": 0},
		{"reducedMotion": true},
		{"click": "button.toggle"},
		{"click": "button.toggle"},
		{"assertCount": "p.note", "equals": 0},
		{"click": "button.reverse"},
		{"click": "button.probe"},
		{"assertGlobal": "__running", "equals": 0},
	})
}
