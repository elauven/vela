package jsast

// Robustness: the parser must never panic or hang, whatever the input, and
// realistic programs must parse without diagnostics.

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// ctCorpus returns every valid snippet from the feature tables plus the
// real-world programs, de-duplicated.
func ctCorpus() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, tbl := range [][]ctCase{ctES5, ctES2015, ctES2016to2019, ctES2020plus, ctLexical} {
		for _, c := range tbl {
			add(c.Src)
		}
	}
	for _, c := range ctInvalid {
		add(c.Src)
	}
	for _, rw := range ctRealWorld {
		add(rw.Src)
	}
	return out
}

func ctNoCrash(t *testing.T, src string) bool {
	t.Helper()
	r := ctParse(src)
	if r.hang {
		t.Errorf("parser hung on %q", ctShorten(src))
		return false
	}
	if r.panic != nil {
		t.Errorf("parser panicked on %q: %v", ctShorten(src), r.panic)
		return false
	}
	if r.prog == nil {
		t.Errorf("nil program for %q", ctShorten(src))
		return false
	}
	return true
}

func ctShorten(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// Every prefix of every corpus entry: exercises EOF handling in every
// production (the classic source of index-out-of-range and infinite loops).
func TestCompatTruncatedInputNeverCrashes(t *testing.T) {
	corpus := ctCorpus()
	failures := 0
	for _, src := range corpus {
		for i := 0; i <= len(src); i++ {
			if !ctNoCrash(t, src[:i]) {
				failures++
				if failures > 5 {
					t.Fatal("too many failures, stopping")
				}
				break
			}
		}
	}
}

// Every suffix (starts in the middle of constructs).
func TestCompatSuffixInputNeverCrashes(t *testing.T) {
	failures := 0
	for _, src := range ctCorpus() {
		for i := 0; i < len(src); i += 1 + len(src)/60 {
			if !ctNoCrash(t, src[i:]) {
				failures++
				if failures > 5 {
					t.Fatal("too many failures, stopping")
				}
				break
			}
		}
	}
}

// Random token-level mutations with a fixed seed (deterministic).
func TestCompatMutatedInputNeverCrashes(t *testing.T) {
	corpus := ctCorpus()
	rng := rand.New(rand.NewSource(20260929))
	frags := []string{"{", "}", "(", ")", "[", "]", ";", ",", ".", "...", "?", "?.", ":", "=>", "=", "`", "${", "'", "\"", "/", "/*", "//",
		"\\", "#", "@", "\n", " ", "async", "await", "yield", "function", "class", "import", "export", "new", "for", "of", "in", "static",
		"get", "set", "let", "const", "if", "else", "return", "0x", "1e", "1_", "\u00A0", "é", "\uFEFF", "\u2028"}
	failures := 0
	for iter := 0; iter < 6000 && failures < 5; iter++ {
		src := corpus[rng.Intn(len(corpus))]
		for m := 0; m < 1+rng.Intn(3); m++ {
			pos := 0
			if len(src) > 0 {
				pos = rng.Intn(len(src) + 1)
			}
			switch rng.Intn(4) {
			case 0: // insert fragment
				src = src[:pos] + frags[rng.Intn(len(frags))] + src[pos:]
			case 1: // delete a slice
				end := pos + rng.Intn(6)
				if end > len(src) {
					end = len(src)
				}
				src = src[:pos] + src[end:]
			case 2: // duplicate a slice
				end := pos + rng.Intn(12)
				if end > len(src) {
					end = len(src)
				}
				src = src[:end] + src[pos:end] + src[end:]
			case 3: // replace a byte
				if len(src) > 0 && pos < len(src) {
					src = src[:pos] + frags[rng.Intn(len(frags))] + src[pos+1:]
				}
			}
		}
		if !ctNoCrash(t, src) {
			failures++
			t.Logf("reproduce with: %q", src)
		}
	}
}

func TestCompatPathologicalShapes(t *testing.T) {
	rep := strings.Repeat
	cases := []struct{ name, src string }{
		// The arrow-function look-ahead scans to the matching ")": these used to be
		// quadratic (10k levels ≈ several seconds) before results were cached.
		{"deep parens", rep("(", 30000) + "a" + rep(")", 30000)},
		{"deep parens, unclosed", rep("(", 30000)},
		{"deep call args in parens", rep("f((", 15000) + "a" + rep("))", 15000)},
		{"deep async(", rep("async(", 20000)},
		{"deep mixed brackets", rep("([{(", 8000)},
		{"chain of arrow functions", rep("(a)=>", 20000) + "a"},
		{"deep arrays", rep("[", 2000) + rep("]", 2000)},
		{"deep objects", rep("({a:", 800) + "1" + rep("})", 800)},
		{"deep blocks", rep("{", 2000) + rep("}", 2000)},
		{"deep nested functions", rep("function f(){", 500) + rep("}", 500)},
		{"deep nested arrows", rep("x=>", 3000) + "x"},
		{"deep unary", rep("!", 5000) + "a"},
		{"deep ternary", rep("a?b:", 2000) + "c"},
		{"deep member chain", "a" + rep(".b", 20000)},
		{"deep call chain", "a" + rep("()", 20000)},
		{"deep nested templates", strings.Repeat("`${", 120) + "a" + strings.Repeat("}`", 120)},
		{"long binary chain", "a" + rep("+a", 30000)},
		{"long array", "[" + rep("1,", 60000) + "]"},
		{"many statements", rep("a = 1;\n", 30000)},
		{"many stray closers", rep(")", 5000)},
		{"many stray openers in template", "`" + rep("${", 2000)},
		{"long string", "'" + rep("x", 200000) + "'"},
		{"long comment", "/*" + rep("x", 200000) + "*/ a"},
		{"many template placeholders", rep("`a${b}c${d}`;\n", 20000)},
		{"one template, many placeholders", "`" + rep("${a}", 30000) + "`"},
		{"class with many members", "class A {" + rep("m(){} ", 5000) + "}"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			// A wall-clock bound catches quadratic behaviour; the race
			// detector makes everything several times slower.
			limit := 2500 * time.Millisecond
			if raceEnabled {
				limit *= 10
			}
			start := time.Now()
			ctNoCrash(t, c.src)
			if d := time.Since(start); d > limit {
				t.Errorf("took %v (possible quadratic behaviour)", d)
			}
		})
	}
}

func FuzzCompatParse(f *testing.F) {
	for _, s := range ctCorpus() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		r := ctParse(src)
		if r.hang || r.panic != nil || r.prog == nil {
			t.Fatalf("hang=%v panic=%v", r.hang, r.panic)
		}
		var visit func(n JsNode)
		visit = func(n JsNode) {
			if n.NodePos() < 0 || n.NodeEnd() < n.NodePos() || n.NodeEnd() > len(src) {
				t.Fatalf("%s has invalid span [%d,%d) for len %d", ctTypeName(n), n.NodePos(), n.NodeEnd(), len(src))
			}
			for _, c := range ctChildren(n) {
				visit(c)
			}
		}
		visit(r.prog)
	})
}

// ──────────────────────────────────────────────
//  Real-world shaped programs (must parse cleanly)
// ──────────────────────────────────────────────

type ctRealCase struct {
	Name string
	Src  string
	Gap  string
}

var ctRealWorld = []ctRealCase{
	{Name: "component options object", Src: `
import { ref, computed, watch } from 'vue'
import Child from './Child.vue'
import * as utils from '../utils'
import defaultFormatter, { formatDate, formatMoney as fm } from '../format'

export default {
  name: 'UserCard',
  components: { Child },
  props: {
    user: { type: Object, required: true },
    size: { type: String, default: 'md' },
  },
  emits: ['select', 'remove'],
  data() {
    return { loading: false, error: null, items: [] }
  },
  computed: {
    fullName() { return ` + "`${this.user.first} ${this.user.last}`" + ` },
    initials: function () {
      return this.fullName.split(' ').map(([c]) => c).join('')
    },
  },
  watch: {
    'user.id': { handler(id, old) { if (id !== old) this.load() }, immediate: true },
  },
  methods: {
    async load() {
      this.loading = true
      try {
        const res = await fetch(` + "`/api/users/${this.user.id}?full=${!!this.full}`" + `)
        if (!res.ok) throw new Error(res.statusText)
        this.items = (await res.json()).items ?? []
      } catch (e) {
        this.error = e
      } finally {
        this.loading = false
      }
    },
    select(item, ...rest) { this.$emit('select', item, ...rest) },
  },
}
`},
	{Name: "setup-style script", Src: `
import { ref, reactive, onMounted } from 'vue'

const props = defineProps({ id: Number })
const emit = defineEmits(['done'])
const count = ref(0)
const state = reactive({ a: 1, b: { c: [1, 2, 3] } })
const doubled = () => count.value * 2
const { a, b: { c: [first, ...others] } } = state

let timer
onMounted(async () => {
  timer = setInterval(() => { count.value++ }, 1000)
  const { default: chart } = await import('./chart.js')
  chart.render(document.querySelector('#chart'), { data: others.map(x => x ** 2) })
})

function toggle(flag = !count.value, { silent = false } = {}) {
  if (flag && !silent) emit('done', count.value)
  return flag ? 'on' : 'off'
}
`},
	{Name: "modern class-based module", Src: `
import { readFile } from 'node:fs/promises'

const RE_TOKEN = /^(?<name>[a-z_]\w*)\s*=\s*(?<value>.+)$/im
const sleep = ms => new Promise(r => setTimeout(r, ms))

export class Cache extends Map {
  static #instances = 0
  #hits = 0
  ttl = 60_000
  onEvict = null

  static { Cache.#instances = 0 }

  constructor(entries, { ttl = 1_000 } = {}) {
    super(entries)
    this.ttl = ttl
    Cache.#instances++
  }

  get hits() { return this.#hits }
  set hits(v) { this.#hits = v }

  async *scan(prefix = '') {
    for (const [k, v] of this) {
      if (k.startsWith(prefix)) yield [k, v]
      await sleep(0)
    }
  }

  #touch(key) { this.#hits++; return this.get(key)?.value ?? null }

  static isCache(o) { return #hits in o }
}

export async function load(path) {
  const text = await readFile(new URL(path, import.meta.url), 'utf8')
  const out = new Intl.NumberFormat('en', { notation: 'compact' })
  for await (const chunk of text.split('\n')) {
    const m = RE_TOKEN.exec(chunk)
    m?.groups && console.log(out.format(+m.groups.value))
  }
}
`},
	{Name: "ES5 IIFE / prototypes", Src: `
;(function (root, factory) {
  if (typeof define === 'function' && define.amd) { define([], factory) }
  else if (typeof module === 'object' && module.exports) { module.exports = factory() }
  else { root.Lib = factory() }
}(this, function () {
  'use strict'
  var hasOwn = Object.prototype.hasOwnProperty, slice = [].slice
  function Lib(opts) {
    if (!(this instanceof Lib)) return new Lib(opts)
    this.opts = extend({ debug: false }, opts || {})
  }
  Lib.prototype.each = function (arr, fn, ctx) {
    for (var i = 0, n = arr.length; i < n; i++) { if (fn.call(ctx, arr[i], i) === false) break }
    for (var k in arr) if (hasOwn.call(arr, k)) continue
    return this
  }
  function extend(t) {
    slice.call(arguments, 1).forEach(function (s) { for (var k in s) t[k] = s[k] })
    return t
  }
  var re = /["'](.*?)["']|\/\*[\s\S]*?\*\//g
  var half = 10 / 2 / 5
  return Lib
}))
`},
	{Name: "functional utilities and control flow", Src: `
export const pipe = (...fns) => x => fns.reduce((v, f) => f(v), x)
export const curry = fn => function curried(...args) {
  return args.length >= fn.length ? fn(...args) : (...more) => curried(...args, ...more)
}
export const groupBy = (xs, key) => xs.reduce((acc, x) => ((acc[x[key]] ||= []).push(x), acc), {})

function* walk(node, depth = 0) {
  if (!node) return
  yield { node, depth }
  for (const child of node.children ?? []) yield* walk(child, depth + 1)
}

export function classify(n) {
  outer: for (let i = 2; i * i <= n; i++) {
    switch (true) {
      case n % i === 0: break outer
      case i > 1e3: continue outer
      default:
    }
  }
  return n < 0 ? 'neg' : n === 0 ? 'zero' : 'pos'
}

const cfg = { ...defaults, ...(await loadConfig().catch(() => ({}))), list: [...a, ...b] }
label: { if (cfg.debug) break label; console.log(cfg) }
x = a ? b : c
;[1, 2, 3].forEach(n => n)
do { i-- } while (i > 0)
`},
}

func TestCompatRealWorldPrograms(t *testing.T) {
	for _, c := range ctRealWorld {
		c := c
		t.Run("RealWorld/"+c.Name, func(t *testing.T) {
			err := ctCheckValid(c.Src, "")
			ctRecord(ctResult{ID: "RealWorld/" + c.Name, Group: "RealWorld", Name: c.Name, Pass: err == "", Gap: c.Gap, Err: err, Src: fmt.Sprintf("<%d bytes>", len(c.Src))})
			ctVerdict(t, fmt.Sprintf("<%s>", c.Name), c.Gap, err)
		})
	}
}
