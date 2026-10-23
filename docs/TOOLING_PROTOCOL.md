# Compiler service protocol

Tools that compile components (bundler plugins, editors, test runners) call
the compiler through this protocol instead of running a build per file or
parsing the CLI's text. Protocol version: **1**.

The protocol compiles one client component per request. Server rendering
(`--ssr`) is a project build and is not part of it.

From JavaScript, `@govela/compiler` wraps the protocol: `startService()`
starts `vela serve --stdio`, checks the handshake against its own version,
and returns `compile(request)` and `close()`; `compile(request)` runs one
request in a short-lived service. The types are in its `index.d.ts`. The
bundler plugin `@govela/unplugin` is a client of the protocol.

## Commands

```sh
vela serve --stdio [--config file] [--protocol 1] [file flags]
vela compile --path src/App.vela [--stdin] [--config file] [file flags]
```

- `vela serve --stdio` is a long-lived process: newline-delimited JSON on
  stdin and stdout. It writes the handshake, then one response line per
  request line. Requests compile concurrently and the responses come in
  completion order, matched by `id`. At the end of stdin the service
  finishes the requests it has and exits with 0. A client must read
  responses while it sends requests.
- `vela compile` compiles one component and prints one result (the response
  without an `id`). The source comes from `--path`, or from stdin with
  `--stdin`; `--path` still names the component. Exit codes: 0 compiled,
  1 not compiled (see `diagnostics`), 2 usage or configuration error.

*File flags* are the flags of the build command that shape one component:
`--minify`, `--css`, `--sourcemap`, `--target`,
`--unavailable-capabilities`, `--strict`, `--hydration-verify`, `--mode`,
`--runtime-import`, `--check-deps`, `--component-imports`. Two defaults differ from the build
command, because the clients bundle the output: `--css=external` (CSS is a
separate result) and `--runtime-import=bare` (imports `@govela/runtime`).

## Handshake

The first line of `vela serve`:

```json
{"protocol":1,"compilerVersion":"0.4.2","runtimePackage":"@govela/runtime","runtimeContract":"$$runtimeContract1"}
```

A client refuses a compiler whose `protocol` it does not speak, and should
check that the installed runtime package has `compilerVersion`. A client can
also start the service with `--protocol N`: when the compiler does not
speak N, the handshake carries `error` and the process exits with 2.

## Request

```json
{"id": 1, "method": "compile", "path": "src/App.vela", "source": "<template>…</template>", "options": {"sourcemap": true}}
```

| Field | |
| --- | --- |
| `id` | Any JSON value, echoed in the response. |
| `method` | `compile` (the default when absent). |
| `path` | The component file, absolute or relative to the service's working directory. |
| `source` | The text to compile, for unsaved editor buffers or transformed sources. Without it the service reads `path`. |
| `options` | File options, the keys of `vela.config.json` below. |

The path decides what a build run in the working directory would: the scope
class of the component's CSS, the file in diagnostics and the source map
`sources`. A path inside the working directory is used relative to it, so
service output equals `vela` output for the same path and options.

Options resolve in this order, first wins: the request, the flags of
`vela serve` / `vela compile`, `vela.config.json`, the defaults above.
Unknown request fields are an error.

## Response

```json
{"id":1,"ok":true,"js":"…","css":"…","map":{…},"diagnostics":[],"imports":[{"specifier":"./Card.vela","kind":"component","resolved":"src/Card.vela"}]}
```

| Field | |
| --- | --- |
| `ok` | The component compiled. |
| `js` | The module: the build's `.js` file without the `sourceMappingURL` comment. |
| `css` | The scoped CSS with `css: external`, absent when the component has none or CSS is injected. |
| `map` | The v3 source map (a JSON object) with `sourcemap: true`. |
| `diagnostics` | Every diagnostic, also warnings of a compiled component. |
| `imports` | The imports of `<script>` as written: static imports, re-exports, literal `import()`. |
| `error` | Why the component did not compile, or why the request was rejected. |

`imports[].kind` is `component` (a relative `.vela`), `module` (another
relative or absolute path), `runtime` (`vela`, `vela/store`), `package`
(a bare specifier) or `url` (`node:`, `https:` and other schemes).
`resolved` is the file of a relative specifier, in the form of `path`.
Watchers use the list to recompile importers.

In `js`, `.vela` imports point to the compiled `.js` modules, as in a build.
With `componentImports: "vela"` they stay `.vela` imports, for a bundler
plugin that compiles the imported components itself.

## Diagnostics

The CLI prints the same objects with `--diagnostics=json`: one per line on
stderr, also for a component that cannot be read.

```json
{"severity":"error","code":"VELA-EVENT-MODIFIER","file":"src/App.vela","line":3,"column":12,"endLine":3,"endColumn":32,"message":"…","suggestion":"…"}
```

- `severity`: `error`, `warning` or `hint`.
- `code`: the stable `VELA-*` category; absent for diagnostics without one.
- `file`: the component, with `/` separators on every platform.
- `line`, `column`: one-based; columns count UTF-16 code units, as editors
  and source maps do.
- `endLine`, `endColumn`: the end of the range, exclusive, in the same
  coordinates. A diagnostic covers the node it is about when the compiler
  knows its extent (a statement, a call, an import, an attribute, a template
  expression), otherwise the token at its start: an identifier, a tag name,
  a `{…}` block or a string. A diagnostic without a source position (an
  unreadable file) has an empty range at 1:1.
- `suggestion`: the canonical correction, when there is one.

## vela.config.json

The CLI, `vela compile` and `vela serve` read `vela.config.json` from the
working directory, or the file given with `--config`. Command-line flags
override it. Unknown keys are an error. Paths are relative to the file.

```json
{
  "project": "src",
  "out": "build",
  "target": "browser",
  "runtimeImport": "bare",
  "css": "external"
}
```

| Key | Flag |
| --- | --- |
| `project`, `out` | `--project`, `--out` |
| `ssr`, `ssrModule` | `--ssr`, `--ssr-module` |
| `minify`, `css`, `sourcemap`, `target`, `strict`, `mode` | the flag of the same name |
| `unavailableCapabilities` | `--unavailable-capabilities` (an array) |
| `hydrationVerify`, `runtimeImport`, `checkDeps` | `--hydration-verify`, `--runtime-import`, `--check-deps` |
| `componentImports` | `--component-imports` (`js` or `vela`) |
| `$schema` | ignored, for editors |

The file options (all keys except `project`, `out`, `ssr`, `ssrModule`)
also apply to the service.
