#!/usr/bin/env node

import process from "node:process";
import { requireLaunchableChromium, requiredTests, verifyGoTestResults } from "./run-ssr-browser-baseline.mjs";

function events(actions) {
  return actions.map(({ name, action }) => JSON.stringify({ Test: name, Action: action })).join("\n");
}

async function expectFailure(label, callback) {
  try {
    await callback();
  } catch {
    return;
  }
  throw new Error(`${label} unexpectedly succeeded`);
}

async function main() {
  const passing = events(requiredTests.map((name) => ({ name, action: "run" })));
  verifyGoTestResults(passing, 0);
  await expectFailure("zero selected tests", () => verifyGoTestResults("", 0));
  await expectFailure("skipped required test", () => verifyGoTestResults(events([
    { name: requiredTests[0], action: "run" },
    { name: requiredTests[0], action: "skip" },
  ]), 0));
  await expectFailure("failed required test", () => verifyGoTestResults(events([
    { name: requiredTests[0], action: "run" },
    { name: requiredTests[0], action: "fail" },
  ]), 1));
  await expectFailure("missing Playwright", () => requireLaunchableChromium({ moduleName: "vela-missing-playwright-for-test" }));
  await expectFailure("unlaunchable Chromium", () => requireLaunchableChromium({ executablePath: "/definitely/not/a/chromium-binary" }));
  process.stdout.write("SSR browser baseline self-check passed.\n");
}

main().catch((error) => {
  process.stderr.write(`SSR browser baseline self-check failed: ${error.stack || error.message}\n`);
  process.exitCode = 1;
});
