#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import process from "node:process";
import { pathToFileURL } from "node:url";

// This is deliberately a small, strict gate rather than a replacement for
// optional local browser suites. Every id is a public SSR product-path or
// semantic regression required by the C0 delivery checkpoint.
export const requiredTests = [
  "TestGeneratedSSRRendererShortCircuitsUnsafeCollectionReads",
  "TestGeneratedSSRRendererPreservesNullishTextAndArrayJoin",
  "TestGeneratedSSRRendererMatchesValueTextOracle",
  "TestSSRProjectRejectsSingleSelectWithKnownNullBinding",
  // Block runtime of the hydration architecture (task 039).
  "TestRuntimeBlocksUnderJsdom",
  "TestBlocksEngineMatchesRecordedDOM",
  "TestBlocksEngineRendersEachElse",
  "TestBlocksEngineUpdatesSlotContentInEachRows",
  // Block markup and payload stream of the Go renderer (task 040).
  "TestBlocksSSRMarkupMatchesClientDOM",
  "TestBlocksSSRMarkupParsesInChromium",
  // JavaScript and generated Go agree on every expression row in both tiers (task 044).
  "TestSSRExpressionOracleBothTiers",
  "TestSSRInitOracle",
  // Go host implementations end to end (task 030).
  "TestSSRHostImplementationEndToEnd",
  // Hydration gate H3 (task 041): generative differential test and corpus.
  "TestBlocksGenerativeDifferential",
  "TestBlocksGenerativeDifferentialInChromium",
  "TestBlocksCorpusHydrates",
  // Bundler-ready output: the esbuild recipe of docs/GUIDE.md (task 048).
  "TestBundlerRecipeEsbuild",
  // SSR beta gate (task 031): the corpus through CLI, Go, HTTP and Chromium,
  // and the gate's own accounting.
  "TestSSRGate",
  "TestSSRGateComparison",
  "TestSSRGateFixesRenderLikeClient",
  "TestSSRChildFailurePropagates",
];

export async function requireLaunchableChromium({ moduleName = "playwright", executablePath = "" } = {}) {
  let chromium;
  try {
    ({ chromium } = await import(moduleName));
  } catch (error) {
    throw new Error(`required Playwright dependency is unavailable: ${error.message}`);
  }
  let browser;
  try {
    browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) });
  } catch (error) {
    throw new Error(`required Playwright Chromium cannot launch: ${error.message}`);
  } finally {
    await browser?.close();
  }
}

export function verifyGoTestResults(stdout, status) {
  const seen = new Set();
  const skipped = new Set();
  const failed = new Set();
  for (const line of stdout.split("\n")) {
    if (!line.trim()) continue;
    let event;
    try {
      event = JSON.parse(line);
    } catch {
      continue;
    }
    if (!requiredTests.includes(event.Test)) continue;
    if (event.Action === "run") seen.add(event.Test);
    if (event.Action === "skip") skipped.add(event.Test);
    if (event.Action === "fail") failed.add(event.Test);
  }
  const missing = requiredTests.filter((name) => !seen.has(name));
  const problems = [];
  if (status !== 0) problems.push(`go test exited with status ${status}`);
  if (missing.length) problems.push(`zero selected or missing required tests: ${missing.join(", ")}`);
  if (skipped.size) problems.push(`required tests skipped: ${[...skipped].join(", ")}`);
  if (failed.size) problems.push(`required tests failed: ${[...failed].join(", ")}`);
  if (problems.length) throw new Error(problems.join("\n"));
  return seen.size;
}

export async function main() {
  for (const script of ["tools/verify-ssr-value-text-oracle.mjs", "tools/verify-ssr-attribute-oracle.mjs"]) {
    const oracle = spawnSync(process.execPath, [script], { cwd: process.cwd(), encoding: "utf8" });
    process.stdout.write(oracle.stdout);
    process.stderr.write(oracle.stderr);
    if (oracle.error) throw oracle.error;
    if (oracle.status !== 0) throw new Error(`SSR JavaScript oracle ${script} exited with status ${oracle.status}`);
  }
  await requireLaunchableChromium();
  const runPattern = `^(${requiredTests.join("|")})$`;
  const result = spawnSync("go", ["test", "-json", "-count=1", "-run", runPattern, "."], {
    cwd: process.cwd(),
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  });
  process.stdout.write(result.stdout);
  process.stderr.write(result.stderr);
  if (result.error) throw result.error;
  const selected = verifyGoTestResults(result.stdout, result.status);
  process.stdout.write(`SSR browser baseline passed: ${selected}/${requiredTests.length} required tests executed without skips.\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch((error) => {
    process.stderr.write(`SSR browser baseline failed: ${error.stack || error.message}\n`);
    process.exitCode = 1;
  });
}
