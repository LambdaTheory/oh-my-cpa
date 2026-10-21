import assert from "node:assert/strict";
import test from "node:test";
import { getProvider, normalizeVerificationUrl, PROVIDERS } from "../src/providers.js";
import { loadStartConfig } from "../src/config.js";
import { requestAuthUrl } from "../src/http.js";

test("provider metadata preserves eight account systems and callback ports", () => {
  assert.deepEqual(PROVIDERS.map(({ id, flow, codeKind, callbackPort, needsManualCallback }) =>
    [id, flow, codeKind, callbackPort, needsManualCallback]), [
    ["claude", "redirect_otp", "otp", 54545, false],
    ["codex", "redirect_otp", "otp", 1455, false],
    ["antigravity", "redirect_browser", "none", 51121, false],
    ["devin", "redirect_manual_callback", "none", undefined, true],
    ...["kimi", "kimi-ai", "xai", "meta"].map((id) => [id, "device_code", "device", undefined, false]),
  ]);
});

test("aliases canonicalize before configuration leaves the boundary", () => {
  for (const [alias, canonical] of [[" Anthropic ", "claude"], ["OPENAI", "codex"], ["grok", "xai"]]) {
    assert.equal(getProvider(alias!).id, canonical);
    assert.equal(loadStartConfig({ PROVIDER: alias, CPA_BASE_URL: "http://127.0.0.1", CPA_MANAGEMENT_KEY: "synthetic" }).provider, canonical);
  }
});

test("unknown and non-OAuth providers fail before making auth-url requests", async () => {
  for (const provider of ["gemini", "vertex", "api-keys.openai", "api-keys", "arbitrary"]) {
    await assert.rejects(requestAuthUrl("invalid base URL", "synthetic", provider, 1), /not OAuth|unknown OAuth provider/);
  }
});

test("device verification URLs drop every query parameter and reject unsafe display metadata", () => {
  assert.equal(normalizeVerificationUrl("https://device.example.invalid/activate?user_code=ABCD&access_token=secret"), "https://device.example.invalid/activate");
  for (const value of ["http://example.invalid", "https://user:pass@example.invalid", "https://example.invalid:8443",
    "https://example.invalid/#token", "javascript:alert(1)", "https://example.invalid/<b>", "https://example.invalid/a b"]) {
    assert.throws(() => normalizeVerificationUrl(value), /verification URL/);
  }
});
