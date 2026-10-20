import assert from "node:assert/strict";
import test from "node:test";
import { runFixtureFlow, runManualFixtureFlow } from "../src/fixture.js";

test("fixture fills both forms and captures the unreachable loopback callback", { timeout: 30_000 }, async () => {
  assert.deepEqual(await runFixtureFlow(20_000), {
    code: "fixture-code",
    state: "fixture-state",
  });
});

test("manual fixture lets the operator complete login and captures the callback without reading CODE_FILE", { timeout: 30_000 }, async () => {
  assert.deepEqual(await runManualFixtureFlow(async (page) => {
    await page.getByLabel("Email").fill("fixture@example.invalid");
    await page.getByRole("button", { name: "Continue" }).click();
    await page.getByLabel("Verification code").fill("fixture-code");
    await page.getByRole("button", { name: "Verify" }).click({ noWaitAfter: true });
  }, 20_000), {
    callback: {
      code: "fixture-code",
      state: "fixture-state",
    },
    result: {
      provider: "claude",
      state: "fixture-state",
      status: "ok",
      message: "fixture complete",
    },
  });
});
