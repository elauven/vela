package runtime

import (
	_ "embed"
	"strings"
)

// TypeScript declarations of the runtime (task 082, docs/TYPESCRIPT.md
// TS-6). types/ holds them once; the files of the npm package and the
// declarations of the language server are assembled from it:
//
//   - runtime.d.ts (`@govela/runtime`): core.d.ts and store.d.ts;
//   - runtime-hydrate.d.ts (`@govela/runtime/hydrate`): hydrate.d.ts;
//   - ambient.d.ts (`@govela/runtime/ambient`, in tsconfig `types`): the
//     runes, and the modules `vela` (core, stores and lifecycle hooks) and
//     `vela/store` that component scripts import.

//go:embed types/core.d.ts
var typesCore string

//go:embed types/store.d.ts
var typesStore string

//go:embed types/intrinsics.d.ts
var typesIntrinsics string

//go:embed types/runes.d.ts
var typesRunes string

//go:embed types/hydrate.d.ts
var typesHydrate string

// AmbientTypesSpecifier is what a tsconfig lists in `types` (TS-10.2).
const AmbientTypesSpecifier = PackageName + "/ambient"

const declarationsHeader = "// Generated from runtime/types/ of the Vela compiler repository; do not edit.\n"

// body drops the leading comment lines of a fragment.
func body(fragment string) string {
	lines := strings.Split(fragment, "\n")
	i := 0
	for i < len(lines) && (strings.HasPrefix(lines[i], "//") || strings.TrimSpace(lines[i]) == "") {
		i++
	}
	return strings.Join(lines[i:], "\n")
}

// indent indents every non-empty line by two spaces.
func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "  " + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// RuntimeDeclarations is runtime.d.ts of the npm package.
func RuntimeDeclarations() string {
	return declarationsHeader + "// Types of @govela/runtime: the component API and the stores.\n\n" +
		body(typesCore) + "\n" + body(typesStore)
}

// HydrateDeclarations is runtime-hydrate.d.ts of the npm package.
func HydrateDeclarations() string {
	return declarationsHeader + typesHydrate
}

// AmbientDeclarations is ambient.d.ts of the npm package: a script (no
// top-level import or export), so its module declarations are ambient. The
// language server declares the same text (internal/lsp).
func AmbientDeclarations() string {
	var b strings.Builder
	b.WriteString(declarationsHeader)
	b.WriteString("// The ambient types of Vela component scripts: list \"" + AmbientTypesSpecifier + "\"\n")
	b.WriteString("// in the `types` of tsconfig.json.\n\n")
	b.WriteString(body(typesRunes))
	b.WriteString("\ndeclare module 'vela/store' {\n")
	b.WriteString(indent(body(typesStore)))
	b.WriteString("}\n\ndeclare module 'vela' {\n")
	b.WriteString(indent(body(typesCore) + "\n" + body(typesStore) + "\n" + body(typesIntrinsics)))
	b.WriteString("}\n")
	return b.String()
}
