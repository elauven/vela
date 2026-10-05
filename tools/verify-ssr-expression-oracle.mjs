#!/usr/bin/env node
// JavaScript half of the SSR expression oracle (task 044, V-11.1b): every
// row's stored result must be what JavaScript computes. The Go half is
// TestSSRExpressionOracleBothTiers.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const rows = JSON.parse(readFileSync(resolve(process.argv[2] || 'testdata/ssr_expression_oracle.json'), 'utf8'));
const decode = (value) => (value === '__undefined__' ? undefined : value);
const failures = [];
for (const row of rows) {
  const names = Object.keys(row.values || {});
  let actual;
  try {
    actual = { expected: String(Function(...names, `"use strict"; return (${row.expression});`)(...names.map((n) => decode(row.values[n])))) };
  } catch (error) {
    actual = { throws: error.constructor.name };
  }
  if (actual.expected !== row.expected || actual.throws !== row.throws) {
    failures.push(`${row.name}: ${row.expression} = ${JSON.stringify(actual)}, stored ${JSON.stringify({ expected: row.expected, throws: row.throws })}`);
  }
}
if (failures.length) {
  process.stderr.write(`SSR expression oracle (JavaScript) failed:\n${failures.join('\n')}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write(`SSR expression oracle (JavaScript) passed: ${rows.length} rows.\n`);
}
