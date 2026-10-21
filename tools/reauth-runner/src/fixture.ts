import http from "node:http";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import type { Page } from "playwright";
import { parseCallbackUrl } from "./callback.js";
import { runLiveFlow } from "./live.js";

const EMAIL_PAGE = `<!doctype html><html><body>
  <main><h1>Fixture login</h1><form action="/verify" method="get">
    <label>Email <input type="email" name="email" autocomplete="email" required></label>
    <button type="submit">Continue</button>
  </form></main>
</body></html>`;

const CODE_PAGE = `<!doctype html><html><body>
  <main><h1>Verify email</h1><form action="http://localhost:54545/callback" method="get">
    <label>Verification code <input name="otp" autocomplete="one-time-code" inputmode="numeric" pattern="[0-9]{6}" required></label>
    <input type="hidden" name="code" value="fixture-code">
    <input type="hidden" name="state" value="fixture-state">
    <button type="submit">Verify</button>
  </form></main>
</body></html>`;

const PASSWORD_PAGE = `<!doctype html><html><body>
  <main><h1>Sign in</h1><form>
    <label>Password <input type="password" name="password" required></label>
    <button type="submit">Sign in</button>
  </form></main>
</body></html>`;

async function listen(server: http.Server): Promise<number> {
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("fixture server did not bind a TCP port");
  return address.port;
}

export async function runFixtureFlow(timeoutMs = 30_000): Promise<{ code: string; state: string }> {
  const fixture = await runAutomatedFixtureFlow({ loginEmail: "fixture@example.invalid" }, timeoutMs);
  return fixture.callback;
}

export async function runAutomatedFixtureFlow(
  options: { loginEmail: string | undefined; initialPage?: "email" | "code"; nextPage?: "code" | "password" },
  timeoutMs = 30_000,
): Promise<{
  callback: { code: string; state: string };
  result: Record<string, unknown>;
  submittedEmail: string;
}> {
  let submittedCallback: { code: string; state: string } | undefined;
  let submittedEmail = "";
  let baseUrl = "";
  const server = http.createServer((request, response) => {
    if (request.url?.startsWith("/v8/management/oauth/auth-url")) {
      response.setHeader("Content-Type", "application/json");
      response.end(JSON.stringify({ url: `${baseUrl}/login`, state: "fixture-state" }));
      return;
    }
    if (request.url === "/v8/management/oauth/callback" && request.method === "POST") {
      void (async () => {
        let body = "";
        for await (const chunk of request) body += chunk;
        const redirectUrl = (JSON.parse(body) as { redirect_url?: string }).redirect_url ?? "";
        const callback = parseCallbackUrl(redirectUrl);
        submittedCallback = { code: callback.code, state: callback.state };
        response.setHeader("Content-Type", "application/json");
        response.end("{}");
      })();
      return;
    }
    if (request.url?.startsWith("/v8/management/oauth/status")) {
      response.setHeader("Content-Type", "application/json");
      response.end('{"status":"ok","message":"fixture complete"}');
      return;
    }
    response.setHeader("Content-Type", "text/html; charset=utf-8");
    if (request.url?.startsWith("/verify")) {
      submittedEmail = new URL(request.url, baseUrl).searchParams.get("email") ?? "";
      response.end(options.nextPage === "password" ? PASSWORD_PAGE : CODE_PAGE);
      return;
    }
    response.end(options.initialPage === "code" ? CODE_PAGE : EMAIL_PAGE);
  });
  const port = await listen(server);
  baseUrl = `http://127.0.0.1:${port}`;
  const profileDir = await fs.mkdtemp(path.join(os.tmpdir(), "omc-reauth-automated-"));
  const codeFile = path.join(profileDir, "code");
  try {
    await fs.writeFile(codeFile, "123456\n", "utf8");
    const result = await runLiveFlow({
      cpaBaseUrl: baseUrl,
      cpaManagementKey: "synthetic-management-key",
      provider: "claude",
      isHeadless: true,
      isManual: false,
      shouldIgnoreHttpsErrors: false,
      profileDir,
      loginEmail: options.loginEmail,
      codeFile,
      timeoutMs,
    });
    if (!submittedCallback) throw new Error("automated fixture did not submit the callback to CPA");
    return { callback: submittedCallback, result, submittedEmail };
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
    await fs.rm(profileDir, { recursive: true, force: true });
  }
}

export async function runManualFixtureFlow(
  interact: (page: Page) => Promise<void>,
  timeoutMs = 30_000,
): Promise<{ callback: { code: string; state: string }; result: Record<string, unknown> }> {
  let submittedCallback: { code: string; state: string } | undefined;
  let baseUrl = "";
  const server = http.createServer(async (request, response) => {
    if (request.url?.startsWith("/v8/management/oauth/auth-url")) {
      response.setHeader("Content-Type", "application/json");
      response.end(JSON.stringify({ url: `${baseUrl}/login`, state: "fixture-state" }));
      return;
    }
    if (request.url === "/v8/management/oauth/callback") {
      let body = "";
      for await (const chunk of request) body += chunk;
      const redirectUrl = (JSON.parse(body) as { redirect_url?: string }).redirect_url ?? "";
      const callback = parseCallbackUrl(redirectUrl);
      submittedCallback = { code: callback.code, state: callback.state };
      response.setHeader("Content-Type", "application/json");
      response.end("{}");
      return;
    }
    if (request.url?.startsWith("/v8/management/oauth/status")) {
      response.setHeader("Content-Type", "application/json");
      response.end('{"status":"ok","message":"fixture complete"}');
      return;
    }
    response.setHeader("Content-Type", "text/html; charset=utf-8");
    if (request.url?.startsWith("/verify")) response.end(CODE_PAGE);
    else response.end(EMAIL_PAGE);
  });
  const port = await listen(server);
  baseUrl = `http://127.0.0.1:${port}`;
  const profileDir = await fs.mkdtemp(path.join(os.tmpdir(), "omc-reauth-manual-"));
  try {
    const result = await runLiveFlow({
      cpaBaseUrl: baseUrl,
      cpaManagementKey: "synthetic-management-key",
      provider: "claude",
      isHeadless: true,
      isManual: true,
      shouldIgnoreHttpsErrors: false,
      profileDir,
      loginEmail: "must-not-be-filled@example.invalid",
      codeFile: path.join(profileDir, "must-not-be-read"),
      timeoutMs,
    }, {
      onBrowserReady: async (context) => {
        const page = context.pages()[0] ?? await context.waitForEvent("page");
        await interact(page);
      },
    });
    if (!submittedCallback) throw new Error("manual fixture did not submit the callback to CPA");
    return { callback: submittedCallback, result };
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
    await fs.rm(profileDir, { recursive: true, force: true });
  }
}
