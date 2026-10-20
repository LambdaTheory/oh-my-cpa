import http from "node:http";
import { chromium } from "playwright";
import { captureCallback } from "./callback.js";

const EMAIL_PAGE = `<!doctype html><html><body>
  <main><h1>Fixture login</h1><form action="/verify" method="get">
    <label>Email <input type="email" name="email" autocomplete="email" required></label>
    <button type="submit">Continue</button>
  </form></main>
</body></html>`;

const CODE_PAGE = `<!doctype html><html><body>
  <main><h1>Verify email</h1><form action="http://localhost:54545/callback" method="get">
    <label>Verification code <input name="code" autocomplete="one-time-code" inputmode="numeric" required></label>
    <input type="hidden" name="state" value="fixture-state">
    <button type="submit">Verify</button>
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
  const server = http.createServer((request, response) => {
    response.setHeader("Content-Type", "text/html; charset=utf-8");
    if (request.url?.startsWith("/verify")) response.end(CODE_PAGE);
    else response.end(EMAIL_PAGE);
  });
  const port = await listen(server);
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  const capture = captureCallback(context, timeoutMs);
  try {
    const page = await context.newPage();
    await page.goto(`http://127.0.0.1:${port}/`);
    await page.getByLabel("Email").fill("fixture@example.invalid");
    await page.getByRole("button", { name: "Continue" }).click();
    await page.getByLabel("Verification code").fill("fixture-code");
    await page.getByRole("button", { name: "Verify" }).click({ noWaitAfter: true });
    const callback = await capture.result;
    return { code: callback.code, state: callback.state };
  } finally {
    capture.stop();
    await context.close();
    await browser.close();
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
}
