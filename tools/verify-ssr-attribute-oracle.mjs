#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const oraclePath = resolve(process.argv[2] || "testdata/ssr_attribute_oracle.json");
const cases = JSON.parse(readFileSync(oraclePath, "utf8"));
const failures = [];

for (const { name, kind, expression, expected } of cases) {
  let value;
  try {
    // Test data is repository-owned. This is the JavaScript reference half of
    // the ordinary-attribute and boolean-attribute SSR contract.
    value = Function(`"use strict"; return (${expression});`)();
  } catch (error) {
    failures.push(`${name}: JavaScript evaluation failed: ${error.message || error}`);
    continue;
  }
  const actual = kind === "ordinary" ? String(value) : Boolean(value);
  if (actual !== expected) failures.push(`${name}: ${kind} conversion = ${JSON.stringify(actual)}, want ${JSON.stringify(expected)}`);
}

if (failures.length) {
  process.stderr.write(`SSR attribute JavaScript oracle failed:\n${failures.join("\n")}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write(`SSR attribute JavaScript oracle passed: ${cases.length} cases.\n`);
}
