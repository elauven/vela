#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const oraclePath = resolve(process.argv[2] || "testdata/ssr_checked_binding_oracle.json");
const cases = JSON.parse(readFileSync(oraclePath, "utf8"));
const failures = [];

function evaluate(expression) {
  // Expressions are repository-owned. This is the JavaScript reference half
  // of the bind:checked truthiness and adjacent text-state contract.
  return Function(`"use strict"; return (${expression});`)();
}

for (const testCase of cases) {
  for (const phase of ["initial", "updated"]) {
    let value;
    try {
      value = evaluate(testCase[`${phase}_expression`]);
    } catch (error) {
      failures.push(`${testCase.name}/${phase}: JavaScript evaluation failed: ${error.message || error}`);
      continue;
    }
    const checked = Boolean(value);
    const text = String(value);
    if (checked !== testCase[`${phase}_checked`]) {
      failures.push(`${testCase.name}/${phase}: Boolean(value) = ${JSON.stringify(checked)}, want ${JSON.stringify(testCase[`${phase}_checked`])}`);
    }
    if (text !== testCase[`${phase}_text`]) {
      failures.push(`${testCase.name}/${phase}: String(value) = ${JSON.stringify(text)}, want ${JSON.stringify(testCase[`${phase}_text`])}`);
    }
  }
}

if (failures.length) {
  process.stderr.write(`SSR checked-binding JavaScript oracle failed:\n${failures.join("\n")}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write(`SSR checked-binding JavaScript oracle passed: ${cases.length} cases.\n`);
}
