package main

// Event modifiers (task 101): @event.modifier… on elements, <vela:window>
// and component events, in jsdom and in Chromium.

import (
	"strings"
	"testing"
)

const modifiersChild = `<script>
  function ping() {
    $$dispatch('ping', 1);
  }
</script>
<template><button class="ping" @click={ping}>ping</button></template>`

const modifiersApp = `<script>
  import Child from './Child.vela';
  let log = $state('');
  let mode = $state('a');
  let pick = $derived(mode === 'a' ? () => add('A') : () => add('B'));
  function add(name) {
    log = log + name + ';';
  }
</script>
<template>
  <vela:window @keyup.esc={() => add('win-esc')} />
  <button class="clear" @click={() => (log = '')}>clear</button>
  <form class="form" @submit.prevent={(e) => add('submit:' + e.defaultPrevented)}><button class="submit" type="submit">submit</button></form>
  <div class="outer" @click={() => add('outer')}>
    <button class="stop" @click.stop={() => add('stop')}>stop</button>
    <div class="self" style="padding: 30px" @click.self={() => add('self')}><span class="child">child</span></div>
  </div>
  <div class="order" @click={(e) => add(e.defaultPrevented ? 'prevented' : 'default')}>
    <a class="ps" href="#ps" @click.prevent.self={() => add('ps')}><b>ps</b></a>
    <a class="sp" href="#sp" @click.self.prevent={() => add('sp')}><b>sp</b></a>
  </div>
  <div class="cap" @click.capture={() => add('cap')}><button class="inner" @click={() => add('inner')}>inner</button></div>
  <button class="once" @click.once={() => add('once')}>once</button>
  <button class="mode" @click={() => (mode = mode === 'a' ? 'b' : 'a')}>mode</button>
  <button class="pick" @click.once={pick}>pick</button>
  <button class="trusted" @click.trusted={() => add('trusted')}>trusted</button>
  <input class="key" @keydown.enter={() => add('enter')} @keydown.ctrl.k={() => add('ctrl-k')} @keyup.enter.exact={() => add('exact')} @keydown.esc.delete={() => add('esc-or-delete')} />
  <div class="buttons" @mousedown.middle={() => add('middle')} @mousedown.left.shift={() => add('left-shift')}>buttons</div>
  <div class="scroll" @scroll.passive={() => add('scroll')}>scroll</div>
  <Child @on:ping.once={() => add('ping')} />
  <button class="statement" @click={log += 'stmt;'}>statement</button>
  <p class="log">{log}</p>
</template>`

// modifierSteps run in jsdom and in Chromium; every group starts from an
// empty log.
func modifierSteps() []rtStep {
	var steps []rtStep
	group := func(want string, actions ...rtStep) {
		steps = append(steps, rtStep{"click": "button.clear"})
		steps = append(steps, actions...)
		steps = append(steps, rtStep{"assertText": "p.log", "equals": want})
	}
	group("submit:true;", rtStep{"click": "button.submit"})
	group("stop;", rtStep{"click": "button.stop"})
	group("outer;", rtStep{"click": "span.child"})
	group("self;outer;", rtStep{"dispatch": "div.self", "event": "click", "class": "MouseEvent"})
	// .prevent.self prevents before it checks the target; .self.prevent
	// returns first.
	group("prevented;", rtStep{"click": "a.ps b"})
	group("default;", rtStep{"click": "a.sp b"})
	group("cap;inner;", rtStep{"click": "button.inner"})
	group("once;", rtStep{"click": "button.once"}, rtStep{"click": "button.once"})
	// The handler changes before the first event; .once still holds after.
	group("B;", rtStep{"click": "button.mode"}, rtStep{"click": "button.pick"}, rtStep{"click": "button.mode"}, rtStep{"click": "button.pick"})
	group("", rtStep{"dispatch": "button.trusted", "event": "click", "class": "MouseEvent"})
	group("enter;exact;", rtStep{"press": "input.key", "key": "Enter"})
	group("enter;", rtStep{"press": "input.key", "key": "Shift+Enter"})
	group("ctrl-k;", rtStep{"press": "input.key", "key": "Control+k"})
	group("", rtStep{"press": "input.key", "key": "k"})
	group("esc-or-delete;", rtStep{"press": "input.key", "key": "Backspace"})
	group("esc-or-delete;win-esc;", rtStep{"press": "input.key", "key": "Escape"})
	group("middle;", rtStep{"dispatch": "div.buttons", "event": "mousedown", "class": "MouseEvent", "init": map[string]any{"button": 1}})
	group("", rtStep{"dispatch": "div.buttons", "event": "mousedown", "class": "MouseEvent", "init": map[string]any{"button": 0}})
	group("left-shift;", rtStep{"dispatch": "div.buttons", "event": "mousedown", "class": "MouseEvent", "init": map[string]any{"button": 0, "shiftKey": true}})
	group("scroll;", rtStep{"dispatch": "div.scroll", "event": "scroll"})
	group("ping;", rtStep{"click": "button.ping"}, rtStep{"click": "button.ping"})
	group("stmt;", rtStep{"click": "button.statement"})
	return steps
}

func TestEventModifiersCSR(t *testing.T) {
	assertRuntimeSet(t, "App", map[string]string{"App": modifiersApp, "Child": modifiersChild}, modifierSteps())
}

func TestEventModifiersChromium(t *testing.T) {
	steps := modifierSteps()
	// A real click is trusted.
	steps = append(steps, rtStep{"click": "button.clear"}, rtStep{"click": "button.trusted"}, rtStep{"assertText": "p.log", "equals": "trusted;"})
	runBrowserSources(t, map[string]string{"App.vela": modifiersApp, "Child.vela": modifiersChild}, "App.js", steps)
}

func TestEventModifierDiagnostics(t *testing.T) {
	cases := []struct {
		name, template, message, help string
	}{
		{"unknown", `<button @click.prevnt={f}>x</button>`, "unknown event modifier .prevnt on @click", "Did you mean .prevent?"},
		{"key on click", `<button @click.enter={f}>x</button>`, ".enter checks a key, which click events do not have", "@keydown.enter"},
		{"passive prevent", `<div @wheel.passive.prevent={f}>x</div>`, "cannot be .passive and .prevent", "Remove .passive"},
		{"twice", `<button @click.once.once={f}>x</button>`, "modifier .once is given twice", "Write .once once."},
		{"button on key", `<input @keydown.middle={f} />`, "checks a mouse button", ""},
		{"system on scroll", `<div @scroll.ctrl={f}>x</div>`, "only keyboard and mouse events have", ""},
		{"component", `<Card @on:close.prevent={f} />`, "accepts only the .once modifier", ""},
		{"svelte", `<button on:click|preventDefault|stopPropagation={f}>x</button>`, "Svelte 4 syntax", "@click.prevent.stop"},
		{"window", `<vela:window @keydown.entr={f} />`, "unknown event modifier .entr", "Did you mean .enter?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := "<script>import { Card } from './Card.vela'; function f() {}</script>\n<template>" + c.template + "<p>x</p></template>\n"
			res := (&CompilerService{Cwd: mustGetwd(t)}).Compile(ServiceRequest{Path: "App.vela", Source: &source})
			var all []string
			found := false
			for _, d := range res.Diagnostics {
				all = append(all, d.Code+": "+d.Message+" ("+d.Suggestion+")")
				if d.Code == "VELA-EVENT-MODIFIER" && strings.Contains(d.Message, c.message) && strings.Contains(d.Suggestion, c.help) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no VELA-EVENT-MODIFIER %q / %q in %v", c.message, c.help, all)
			}
		})
	}
}

func TestEventModifiersCompile(t *testing.T) {
	source := `<script>
  function f() {}
</script>
<template>
  <vela:document @keydown.ctrl.shift.f1={f} />
  <input @keydown.page-down.home={f} @keyup.7={f} @keydown.space.exact={f} />
  <div @pointerdown.right.capture={f} @touchstart.passive={f} @click.trusted.self.stop={f}>x</div>
</template>
`
	res := (&CompilerService{Cwd: mustGetwd(t)}).Compile(ServiceRequest{Path: "App.vela", Source: &source})
	if res.Error != "" {
		t.Fatalf("compile: %s %v", res.Error, res.Diagnostics)
	}
	for _, d := range res.Diagnostics {
		if d.Severity == "error" {
			t.Errorf("unexpected %s: %s", d.Code, d.Message)
		}
	}
}
