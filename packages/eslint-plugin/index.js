// @govela/eslint-plugin: ESLint for the JavaScript and TypeScript of Vela
// components (task 067). The processor lints the virtual document of a
// component, which `vela document` writes: the <script> as written, then the
// template expressions in a function whose blocks declare the {#each} and
// {#await} names. A variable used only in the template is therefore used,
// and a mistake in a template expression is reported at its place in the
// .vela file. Messages about the generated text are dropped.
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { binaryPath } from '@govela/compiler';

const manifest = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8'));

/** The compiler intrinsics, as globals of the components' code. */
export const globals = {
  $state: 'readonly',
  $derived: 'readonly',
  $effect: 'readonly',
  $bindable: 'readonly',
  $$dispatch: 'readonly',
};

// The documents of the files being linted, from preprocess to postprocess.
const documents = new Map();

let binary;
function velaBinary() {
  binary ??= binaryPath();
  return binary;
}

function lineStarts(text) {
  const starts = [0];
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\n') starts.push(i + 1);
  }
  return starts;
}

function offsetOf(starts, line, column) {
  const start = starts[Math.min(Math.max(line, 1), starts.length) - 1];
  return start + Math.max(column - 1, 0);
}

function positionOf(starts, offset) {
  let lo = 0;
  let hi = starts.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (starts[mid] <= offset) lo = mid;
    else hi = mid - 1;
  }
  return { line: lo + 1, column: offset - starts[lo] + 1 };
}

// The copied segment holding the virtual offset: an offset inside one, or
// at its end (a missing semicolon after the last statement).
function segmentAt(segments, offset) {
  let found = null;
  for (const segment of segments) {
    const [, virt, length] = segment;
    if (virt <= offset && offset < virt + length) return segment;
    if (offset === virt + length) found ??= segment;
  }
  return found;
}

function document(text, filename) {
  const result = spawnSync(velaBinary(), ['document', '--path', filename, '--stdin'], {
    input: text,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    windowsHide: true,
  });
  if (result.error) throw new Error(`@govela/eslint-plugin: cannot run ${binary}: ${result.error.message}`);
  if (result.status !== 0) throw new Error(`@govela/eslint-plugin: vela document ${filename}: ${result.stderr.trim()}`);
  return JSON.parse(result.stdout);
}

function preprocess(text, filename) {
  const doc = document(text, filename);
  documents.set(filename, {
    source: text,
    sourceStarts: lineStarts(text),
    virtualStarts: lineStarts(doc.text),
    segments: doc.segments,
  });
  const base = filename.split(/[\\/]/).pop();
  return [{ text: doc.text, filename: `${base}.${doc.typescript ? 'ts' : 'js'}` }];
}

// mapRange maps a virtual range into the component; null when it does not
// start in copied text. The end is clipped to the segment of the start.
function mapRange(doc, start, end) {
  const segment = segmentAt(doc.segments, start);
  if (!segment) return null;
  const [src, virt, length] = segment;
  const clipped = Math.min(Math.max(end, start), virt + length);
  return [src + start - virt, src + clipped - virt];
}

// mapFix maps a fix whose range lies in one segment; others are dropped.
function mapFix(doc, fix) {
  if (!fix) return undefined;
  const [start, end] = fix.range;
  const segment = segmentAt(doc.segments, start);
  if (!segment) return null;
  const [src, virt, length] = segment;
  if (end > virt + length) return null;
  return { range: [src + start - virt, src + end - virt], text: fix.text };
}

function mapMessage(doc, message) {
  if (!message.line) return message;
  const start = offsetOf(doc.virtualStarts, message.line, message.column);
  const end = message.endLine ? offsetOf(doc.virtualStarts, message.endLine, message.endColumn) : start;
  const range = mapRange(doc, start, end);
  if (!range) {
    // A parse error stays visible, at the start of the file.
    return message.fatal ? { ...message, line: 1, column: 1, endLine: undefined, endColumn: undefined } : null;
  }
  const from = positionOf(doc.sourceStarts, range[0]);
  const mapped = { ...message, line: from.line, column: from.column };
  if (message.endLine) {
    const to = positionOf(doc.sourceStarts, range[1]);
    mapped.endLine = to.line;
    mapped.endColumn = to.column;
  }
  if ('fix' in message) {
    const fix = mapFix(doc, message.fix);
    if (fix) mapped.fix = fix;
    else delete mapped.fix;
  }
  if (message.suggestions) {
    mapped.suggestions = message.suggestions
      .map((s) => ({ ...s, fix: mapFix(doc, s.fix) }))
      .filter((s) => s.fix);
  }
  return mapped;
}

function postprocess(messages, filename) {
  const doc = documents.get(filename);
  documents.delete(filename);
  if (!doc) return messages.flat();
  return messages.flat().map((message) => mapMessage(doc, message)).filter(Boolean);
}

/** The processor: `processor: 'vela/vela'` for `**\/*.vela`. */
export const processor = {
  meta: { name: 'vela', version: manifest.version },
  preprocess,
  postprocess,
  supportsAutofix: true,
};

const plugin = {
  meta: { name: manifest.name, version: manifest.version },
  processors: { vela: processor },
  configs: {},
};

/**
 * The recommended flat config: the processor for .vela files and the
 * intrinsics as globals of their code. Add your own rules after it, for
 * `**\/*.vela/*.js` (and `**\/*.vela/*.ts` with typescript-eslint).
 */
plugin.configs.recommended = [
  {
    name: 'vela/processor',
    files: ['**/*.vela'],
    plugins: { vela: plugin },
    processor: 'vela/vela',
  },
  {
    name: 'vela/globals',
    files: ['**/*.vela/*.js', '**/*.vela/*.ts'],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals,
    },
  },
];

export default plugin;
