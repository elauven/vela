#!/usr/bin/env node
// JavaScript half of the SSR initialization oracle (task 044): component
// initialization in declaration order (T-6.6d) and object key order
// (T-5.1c). For every row it computes the template text and the JSON of the
// "s" payload entry (state bindings in order, then $bindable props), and
// with --fill writes them into the corpus; otherwise it checks the stored
// results. The Go half is TestSSRInitOracle.
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const path = resolve(process.argv.find((a, i) => i > 1 && !a.startsWith('--')) || 'testdata/ssr_init_oracle.json');
const fill = process.argv.includes('--fill');
const rows = JSON.parse(readFileSync(path, 'utf8'));
const decode = (value) => (value === '__undefined__' ? undefined : value);

function evaluate(row) {
  const lines = ['"use strict";'];
  const states = [];
  const bindables = [];
  for (const [kind, name, init] of row.decls) {
    if (kind === 'prop' || kind === 'bindable') {
      lines.push(`let ${name} = Object.prototype.hasOwnProperty.call(props, ${JSON.stringify(name)}) ? props[${JSON.stringify(name)}] : (${init});`);
      if (kind === 'bindable') bindables.push(name);
    } else {
      lines.push(`${kind === 'const' ? 'const' : 'let'} ${name} = (${init});`);
      if (kind === 'state' || kind === 'deep') states.push(name);
    }
  }
  const stateObject = `({${[...states, ...bindables].map((n) => `${JSON.stringify(n)}: ${n}`).join(', ')}})`;
  lines.push(`return [String(${row.expression}), JSON.stringify(${stateObject})];`);
  const props = Object.fromEntries(Object.entries(row.props || {}).map(([k, v]) => [k, decode(v)]));
  const [text, state] = Function('props', lines.join('\n'))(props);
  return { expected: text, state };
}

const failures = [];
for (const row of rows) {
  const actual = evaluate(row);
  if (fill) {
    Object.assign(row, actual);
    continue;
  }
  if (actual.expected !== row.expected || actual.state !== row.state) {
    failures.push(`${row.name}: computed ${JSON.stringify(actual)}, stored ${JSON.stringify({ expected: row.expected, state: row.state })}`);
  }
}
if (fill) {
  writeFileSync(path, '[\n' + rows.map((r) => ' ' + JSON.stringify(r)).join(',\n') + '\n]\n');
} else if (failures.length) {
  process.stderr.write(`SSR init oracle (JavaScript) failed:\n${failures.join('\n')}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write(`SSR init oracle (JavaScript) passed: ${rows.length} rows.\n`);
}
