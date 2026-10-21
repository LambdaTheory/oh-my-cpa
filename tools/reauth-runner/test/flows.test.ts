import assert from "node:assert/strict";
import fs from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import type { StartConfig } from "../src/config.js";
import { runLiveFlow, type LiveFlowHooks } from "../src/live.js";
import { getProvider } from "../src/providers.js";
import { requestAuthUrl } from "../src/http.js";

interface FixtureOptions {
  provider: string;
  hasJob?: boolean;
  isOmcDown?: boolean;
  omcError?: number;
  terminalStatus?: string;
  hasEmptyQueue?: boolean;
  isExternalCallback?: boolean;
  hasCallbackConflict?: boolean;
  hasMissingDeviceCode?: boolean;
  hasCodeOutage?: boolean;
  expiresIn?: number;
}

async function createFixture(options: FixtureOptions) {
  const provider = getProvider(options.provider);
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "reauth-provider-test-"));
  const requests: Array<{ url: URL; body: Record<string, unknown>; cookie?: string; origin?: string }> = [];
  const reports: Array<Record<string, unknown>> = [];
  const notices: string[] = [];
  let baseUrl = "";
  let hasAuthorized = false;
  let codeReads = 0;
  let failure: Error | undefined;
  const callbackUrl = `http://localhost:${provider.callbackPort ?? 8317}/callback?code=synthetic-grant&state=fixture-state`;
  const server = http.createServer(async (request, response) => {
    try {
      const url = new URL(request.url!, baseUrl);
      let rawBody = "";
      for await (const chunk of request) rawBody += chunk;
      const body = rawBody ? JSON.parse(rawBody) as Record<string, unknown> : {};
      requests.push({ url, body, cookie: request.headers.cookie, origin: request.headers.origin });
      response.setHeader("Content-Type", "application/json");
      if (url.pathname === "/omc/api/auth/login") {
        assert.deepEqual(body, { password: "synthetic-omc-key" });
        if (options.isOmcDown || options.omcError) { response.writeHead(options.omcError ?? 503).end(); return; }
        response.setHeader("Set-Cookie", "omc_session=synthetic-session; Path=/omc; HttpOnly");
        response.end("{}"); return;
      }
      if (url.pathname.startsWith("/omc/api/v1/")) {
        assert.equal(request.headers.cookie, "omc_session=synthetic-session");
        assert.equal(request.headers.origin, baseUrl);
        if (url.pathname.endsWith("/next")) {
          assert.equal(url.searchParams.get("provider"), provider.id);
          if (options.hasEmptyQueue) { response.writeHead(204).end(); return; }
          response.end(JSON.stringify({ id: "job-fixture", provider: provider.id, flow: provider.flow, code_kind: provider.codeKind, state: "pending" })); return;
        }
        if (url.pathname.endsWith("/status")) {
          if (body.status === "code_submitted") assert.ok(codeReads > 0);
          reports.push(body); response.end("{}"); return;
        }
        if (url.pathname.endsWith("/code")) {
          assert.equal(provider.codeKind, "otp");
          codeReads += 1;
          if (options.hasCodeOutage) { response.writeHead(503).end(); return; }
          if (codeReads === 1) { response.writeHead(204).end(); return; }
          response.end(JSON.stringify({ code: "123456" })); return;
        }
      }
      if (url.pathname.startsWith("/v8/")) assert.equal(request.headers.authorization, "Bearer synthetic-cpa-key");
      if (url.pathname.endsWith("/oauth/auth-url")) {
        assert.equal(url.searchParams.get("provider"), provider.id);
        assert.equal(url.searchParams.get("is_webui"), provider.callbackPort ? "true" : null);
        response.end(JSON.stringify(provider.codeKind === "device" ? {
          flow: "device", url: "https://device.example.invalid/activate?user_code=ABCD-EFGH&access_token=must-not-escape",
          user_code: options.hasMissingDeviceCode ? undefined : "ABCD-EFGH", expires_in: options.expiresIn ?? 300, state: "fixture-state",
        } : { url: `${baseUrl}/login`, state: "fixture-state" })); return;
      }
      if (url.pathname.endsWith("/oauth/callback")) {
        assert.equal(body.provider, provider.id);
        assert.equal(body.redirect_url, callbackUrl);
        hasAuthorized = true;
        if (options.hasCallbackConflict) { response.writeHead(409).end(); return; }
        response.end("{}"); return;
      }
      if (url.pathname.endsWith("/oauth/status")) {
        assert.equal(url.searchParams.get("state"), "fixture-state");
        assert.equal(url.searchParams.get("session_id"), "fixture-state");
        response.end(JSON.stringify({ status: options.terminalStatus ?? (hasAuthorized ? "ok" : "wait"), message: "must-not-escape" })); return;
      }
      response.setHeader("Content-Type", "text/html");
      if (provider.codeKind === "otp") {
        if (url.pathname === "/login") {
          response.end('<form action="/otp"><label>Email<input type="email" name="email"></label><button>Continue</button></form>');
        } else if (url.pathname === "/otp") {
          assert.equal(url.searchParams.get("email"), "fixture@example.invalid");
          response.end('<form action="/verify"><label>Code<input autocomplete="one-time-code" name="otp"></label><button>Verify</button></form>');
        } else if (url.pathname === "/verify") {
          assert.equal(url.searchParams.get("otp"), "123456");
          response.writeHead(302, { Location: callbackUrl }).end();
        } else response.writeHead(404).end();
      } else {
        response.end(`<a href="${callbackUrl.replaceAll("&", "&amp;")}">Complete login</a>`);
      }
    } catch (error) {
      failure = error as Error;
      response.writeHead(500).end();
    }
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("fixture listen failed");
  baseUrl = `http://127.0.0.1:${address.port}`;
  const config: StartConfig = {
    cpaBaseUrl: baseUrl, cpaManagementKey: "synthetic-cpa-key", provider: options.provider,
    profileDir: path.join(directory, "profile"), isHeadless: true, isManual: false,
    shouldIgnoreHttpsErrors: false, loginEmail: "fixture@example.invalid",
    codeFile: path.join(directory, "must-not-be-read"), timeoutMs: 10_000,
    ...(options.hasJob ? { omcBaseUrl: `${baseUrl}/omc`, omcManagementKey: "synthetic-omc-key" } : {}),
  };
  const hooks: LiveFlowHooks = {
    onNotice: (message) => notices.push(message),
    onBrowserReady: async (context) => {
      if (provider.codeKind === "device") {
        await context.route("https://device.example.invalid/**", async (route) => {
          assert.equal(route.request().url(), "https://device.example.invalid/activate");
          hasAuthorized = true;
          await route.fulfill({ contentType: "text/html", body: "<h1>Device approved</h1>" });
        });
      } else if (provider.codeKind === "none") {
        const page = context.pages()[0]!;
        await page.getByRole("link", { name: "Complete login" }).waitFor();
        if (options.isExternalCallback) hasAuthorized = true;
        else await page.getByRole("link").click({ noWaitAfter: true });
      }
    },
  };
  return { config, hooks, requests, reports, notices,
    close: async () => {
      await new Promise<void>((resolve) => server.close(() => resolve()));
      await fs.rm(directory, { recursive: true, force: true });
      if (failure) throw failure;
    },
  };
}

for (const provider of ["anthropic", "openai", "antigravity", "devin", "kimi", "kimi-ai", "grok", "meta"]) {
  test(`${provider} fixture uses canonical job claim, flow-specific interaction, and success report`, async () => {
    const fixture = await createFixture({ provider, hasJob: true });
    try {
      const result = await runLiveFlow(fixture.config, fixture.hooks);
      const metadata = getProvider(provider);
      assert.deepEqual(result, { provider: metadata.id, state: "fixture-state", status: "ok" });
      assert.deepEqual(fixture.reports.map((report) => report.status), metadata.codeKind === "otp"
        ? ["oauth_started", "code_submitted", "succeeded"] : ["oauth_started", "succeeded"]);
      const codeCalls = fixture.requests.filter((request) => request.url.pathname.endsWith("/code"));
      assert.equal(codeCalls.length, metadata.codeKind === "otp" ? 2 : 0);
      assert.equal(fixture.requests.filter((request) => request.url.pathname.endsWith("/login") && request.url.pathname.includes("/api/")).length, 1);
      if (metadata.codeKind === "device") {
        assert.deepEqual(fixture.reports[0], { status: "oauth_started", flow: "device_code", user_code: "ABCD-EFGH", verification_url: "https://device.example.invalid/activate", expires_in: 300 });
        assert.equal(fixture.requests.filter((request) => request.url.pathname.endsWith("/callback")).length, 0);
        assert.match(fixture.notices.join(" "), /ABCD-EFGH.*300s/);
      }
      if (metadata.needsManualCallback) assert.match(fixture.notices.join(" "), /no callback forwarder.*tunnel.*submit/);
      assert.ok(!JSON.stringify([result, fixture.reports, fixture.notices]).includes("must-not-escape"));
    } finally { await fixture.close(); }
  });
}

test("Devin waits for an externally submitted callback without requiring CODE_FILE", async () => {
  const fixture = await createFixture({ provider: "devin", hasJob: true, isExternalCallback: true });
  try {
    assert.equal((await runLiveFlow(fixture.config, fixture.hooks)).status, "ok");
    assert.equal(fixture.requests.filter((request) => request.url.pathname.endsWith("/callback")).length, 0);
  } finally { await fixture.close(); }
});

test("reachable forwarder already-completed response requires positive CPA status", async () => {
  const fixture = await createFixture({ provider: "claude", hasJob: true, hasCallbackConflict: true });
  try { assert.equal((await runLiveFlow(fixture.config, fixture.hooks)).status, "ok"); }
  finally { await fixture.close(); }
});

test("empty OMC queue does not start a CPA session", async () => {
  const fixture = await createFixture({ provider: "claude", hasJob: true, hasEmptyQueue: true });
  try {
    assert.deepEqual(await runLiveFlow(fixture.config, fixture.hooks), { provider: "claude", status: "idle" });
    assert.equal(fixture.requests.length, 2);
  } finally { await fixture.close(); }
});

for (const hasCodeOutage of [false, true]) {
  test(`OMC ${hasCodeOutage ? "code" : "login"} outage warns and allows standalone code file`, async () => {
    const fixture = await createFixture({ provider: "claude", hasJob: true, isOmcDown: !hasCodeOutage, hasCodeOutage });
    try {
      await fs.writeFile(fixture.config.codeFile!, "123456");
      assert.equal((await runLiveFlow(fixture.config, fixture.hooks)).status, "ok");
      assert.match(fixture.notices.join(" "), /Warning: OMC.*CODE_FILE/);
      assert.equal(fixture.reports.some((report) => report.status === "code_submitted"), false);
    } finally { await fixture.close(); }
  });
}

test("OMC authorization rejection does not bypass the job boundary", async () => {
  const fixture = await createFixture({ provider: "claude", hasJob: true, omcError: 403 });
  try {
    await assert.rejects(runLiveFlow(fixture.config, fixture.hooks), /HTTP 403/);
    assert.equal(fixture.requests.length, 1);
  } finally { await fixture.close(); }
});

for (const terminalStatus of ["error", "cancelled", "expired", "invalid"]) {
  test(`CPA ${terminalStatus} cannot produce a succeeded report`, async () => {
    const fixture = await createFixture({ provider: "meta", hasJob: true, terminalStatus });
    try {
      await assert.rejects(runLiveFlow(fixture.config, fixture.hooks), /OAuth.*status/);
      assert.deepEqual(fixture.reports.map((report) => report.status), ["oauth_started", "failed"]);
      assert.equal(fixture.reports[1]?.last_error, "runner_failed");
    } finally { await fixture.close(); }
  });
}

test("missing device metadata fails before opening a browser or reporting oauth_started", async () => {
  const fixture = await createFixture({ provider: "kimi", hasMissingDeviceCode: true });
  try { await assert.rejects(requestAuthUrl(fixture.config.cpaBaseUrl, fixture.config.cpaManagementKey, "kimi", 1000), /device response requires/); }
  finally { await fixture.close(); }
});

for (const isTimeout of [true, false]) {
  test(`${isTimeout ? "deadline" : "cancellation"} stops device polling and reports failure`, async () => {
    const fixture = await createFixture({ provider: "meta", hasJob: true, terminalStatus: "wait" });
    const controller = new AbortController();
    try {
      if (isTimeout) fixture.config.timeoutMs = 1500;
      else {
        const prepare = fixture.hooks.onBrowserReady!;
        fixture.hooks.onBrowserReady = async (context) => {
          await prepare(context);
          controller.abort(new Error("OAuth run cancelled by fixture"));
        };
      }
      await assert.rejects(runLiveFlow(fixture.config, { ...fixture.hooks, signal: controller.signal }), isTimeout ? /timed out/ : /cancelled/);
      assert.deepEqual(fixture.reports.map((report) => report.status), ["oauth_started", "failed"]);
    } finally { await fixture.close(); }
  });
}

test("device expiry bounds polling even when TIMEOUT_MS is longer", async () => {
  const fixture = await createFixture({ provider: "meta", hasJob: true, terminalStatus: "wait", expiresIn: 1 });
  try {
    await assert.rejects(runLiveFlow(fixture.config, fixture.hooks), /device authorization expired/);
    assert.deepEqual(fixture.reports.map((report) => report.status), ["oauth_started", "failed"]);
  } finally { await fixture.close(); }
});

test("closing the browser cancels the job and does not wait for the run deadline", async () => {
  const fixture = await createFixture({ provider: "meta", hasJob: true });
  try {
    fixture.hooks.onBrowserReady = async (context) => { await context.close(); };
    await assert.rejects(runLiveFlow(fixture.config, fixture.hooks), /cancelled: browser was closed/);
    assert.deepEqual(fixture.reports.map((report) => report.status), ["oauth_started", "failed"]);
  } finally { await fixture.close(); }
});

test("MANUAL OTP job leaves input and code retrieval to the operator", async () => {
  const fixture = await createFixture({ provider: "claude", hasJob: true });
  fixture.config.isManual = true;
  fixture.hooks.onBrowserReady = async (context) => {
    const page = context.pages()[0]!;
    await page.getByLabel("Email").fill("fixture@example.invalid");
    await page.getByRole("button", { name: "Continue" }).click();
    await page.getByLabel("Code").fill("123456");
    await page.getByRole("button", { name: "Verify" }).click({ noWaitAfter: true });
  };
  try {
    assert.equal((await runLiveFlow(fixture.config, fixture.hooks)).status, "ok");
    assert.equal(fixture.requests.some((request) => request.url.pathname.endsWith("/code")), false);
    assert.deepEqual(fixture.reports.map((report) => report.status), ["oauth_started", "succeeded"]);
  } finally { await fixture.close(); }
});

test("OTP waiting for a missing CODE_FILE ends at the global deadline", async () => {
  const fixture = await createFixture({ provider: "claude" });
  fixture.config.timeoutMs = 1500;
  try { await assert.rejects(runLiveFlow(fixture.config, fixture.hooks), /timed out/); }
  finally { await fixture.close(); }
});
