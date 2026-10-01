#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const oraclePath = resolve(process.argv[2] || "testdata/ssr_value_text_oracle.json");
const cases = JSON.parse(readFileSync(oraclePath, "utf8"));
const failures = [];

for (const { name, expression, expected } of cases) {
  let actual;
  try {
    // The corpus is repository-owned test data. Evaluating it is intentional:
    // this script is the JavaScript half of the SSR semantic oracle.
    actual = String(Function(`"use strict"; return (${expression});`)());
  } catch (error) {
    failures.push(`${name}: JavaScript evaluation failed: ${error.message || error}`);
    continue;
  }
  if (actual !== expected) failures.push(`${name}: String(${expression}) = ${JSON.stringify(actual)}, want ${JSON.stringify(expected)}`);
}

if (failures.length) {
  process.stderr.write(`SSR value-text JavaScript oracle failed:\n${failures.join("\n")}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write(`SSR value-text JavaScript oracle passed: ${cases.length} cases.\n`);
}
