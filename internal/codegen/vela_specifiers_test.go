package codegen

import "testing"

func TestRewriteJSModuleSpecifiers(t *testing.T) {
	cases := map[string]string{
		"import App from './App.vela';":                       "import App from './App.js';",
		`import { A, B } from "../ui/Card.vela"`:              `import { A, B } from "../ui/Card.js"`,
		"import './Side.vela';":                               "import './Side.js';",
		"export { default as Card } from './Card.vela';":      "export { default as Card } from './Card.js';",
		"export * from './All.vela';":                         "export * from './All.js';",
		"const Page = () => import('./Page.vela');":           "const Page = () => import('./Page.js');",
		"import data from './d.json' with { type: 'json' };":  "import data from './d.json' with { type: 'json' };",
		"import { format } from 'date-fns';":                  "import { format } from 'date-fns';",
		"const s = './App.vela'; f('./B.vela');":              "const s = './App.vela'; f('./B.vela');",
		"const t = `import App from './App.vela'`;":           "const t = `import App from './App.vela'`;",
		"// import App from './App.vela'\nlet x = 1 / 2 / 3;": "// import App from './App.vela'\nlet x = 1 / 2 / 3;",
		"const o = { from: './A.vela' };":                     "const o = { from: './A.vela' };",
		"import { writable } from 'vela/store';":              "import { writable } from './runtime.js';",
		"import { tick } from \"vela\";":                      "import { tick } from \"./runtime.js\";",
		"import x from 'velatile';":                           "import x from 'velatile';",
		"import 'pkg/file.vela';":                             "import 'pkg/file.vela';",
	}
	for src, want := range cases {
		if got := RewriteJSModuleSpecifiers(src, "./runtime.js"); got != want {
			t.Errorf("rewriteJSModuleSpecifiers(%q)\n got  %q\n want %q", src, got, want)
		}
	}
}
