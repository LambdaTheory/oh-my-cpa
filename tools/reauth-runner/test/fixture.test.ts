import assert from "node:assert/strict";
import test from "node:test";
import { runAutomatedFixtureFlow, runFixtureFlow, runManualFixtureFlow } from "../src/fixture.js";

test("fixture fills LOGIN_EMAIL, continues to the code form, and captures the loopback callback", { timeout: 30_000 }, async () => {
  assert.deepEqual(await runFixtureFlow(20_000), {
    code: "fixture-code",
    state: "fixture-state",
  });
});

test("automated fixture submits LOGIN_EMAIL through the production browser flow", { timeout: 30_000 }, async () => {
  assert.deepEqual(await runAutomatedFixtureFlow({ loginEmail: "fixture@example.invalid" }, 20_000), {
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
    submittedEmail: "fixture@example.invalid",
  });
});

test("automated fixture fails clearly when an email is requested without LOGIN_EMAIL", { timeout: 30_000 }, async () => {
  await assert.rejects(
    runAutomatedFixtureFlow({ loginEmail: undefined }, 5_000),
    /LOGIN_EMAIL is required when the authorization page asks for an email address/,
  );
});

test("automated fixture skips LOGIN_EMAIL when an existing session reaches the code form", { timeout: 30_000 }, async () => {
  const fixture = await runAutomatedFixtureFlow({ loginEmail: undefined, initialPage: "code" }, 20_000);
  assert.deepEqual(fixture.callback, { code: "fixture-code", state: "fixture-state" });
  assert.equal(fixture.submittedEmail, "");
});

test("automated fixture asks for manual takeover when password or SSO is required", { timeout: 30_000 }, async () => {
  await assert.rejects(
    runAutomatedFixtureFlow({ loginEmail: "fixture@example.invalid", nextPage: "password" }, 5_000),
    /password\/SSO required; manual takeover needed; retry with MANUAL=true/,
  );
});

test("manual fixture lets the operator complete login and captures the callback without reading CODE_FILE", { timeout: 30_000 }, async () => {
  assert.deepEqual(await runManualFixtureFlow(async (page) => {
    assert.equal(await page.getByLabel("Email").inputValue(), "");
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
