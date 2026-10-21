import assert from "node:assert/strict";
import http from "node:http";
import test from "node:test";
import { requestAuthUrl, submitCallbackToCpa, submitCallbackToOmc } from "../src/http.js";

async function startServer(handler: http.RequestListener): Promise<{ baseUrl: string; close: () => Promise<void> }> {
  const server = http.createServer(handler);
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("test server did not bind a TCP port");
  return {
    baseUrl: `http://127.0.0.1:${address.port}`,
    close: () => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())),
  };
}

test("CPA requests use fixed OAuth endpoints and bearer auth", async () => {
  const requests: Array<{ method?: string; url?: string; auth?: string; body: string }> = [];
  const server = await startServer(async (request, response) => {
    let body = "";
    for await (const chunk of request) body += chunk;
    requests.push({ method: request.method, url: request.url, auth: request.headers.authorization, body });
    response.setHeader("Content-Type", "application/json");
    if (request.method === "GET") response.end('{"url":"https://example.invalid/login","state":"fixture-state"}');
    else response.end('{}');
  });
  try {
    assert.deepEqual(await requestAuthUrl(server.baseUrl, "synthetic-management-key", "claude", 5_000), {
      url: "https://example.invalid/login",
      state: "fixture-state",
    });
    await submitCallbackToCpa(
      server.baseUrl,
      "synthetic-management-key",
      "claude",
      "http://localhost:54545/callback?code=fixture-code&state=fixture-state",
      5_000,
    );
  } finally {
    await server.close();
  }

  assert.equal(requests[0]?.url, "/v8/management/oauth/auth-url?provider=claude&is_webui=true");
  assert.equal(requests[0]?.auth, "Bearer synthetic-management-key");
  assert.equal(requests[1]?.url, "/v8/management/oauth/callback");
  assert.equal(requests[1]?.auth, "Bearer synthetic-management-key");
  assert.deepEqual(JSON.parse(requests[1]?.body ?? ""), {
    provider: "claude",
    redirect_url: "http://localhost:54545/callback?code=fixture-code&state=fixture-state",
  });
});

test("OMC callback submission establishes a same-origin session", async () => {
  const requests: Array<{ url?: string; origin?: string; cookie?: string; body: string }> = [];
  const server = await startServer(async (request, response) => {
    let body = "";
    for await (const chunk of request) body += chunk;
    requests.push({ url: request.url, origin: request.headers.origin, cookie: request.headers.cookie, body });
    response.setHeader("Content-Type", "application/json");
    if (request.url === "/omc/api/auth/login") response.setHeader("Set-Cookie", "omc_session=fixture-session; Path=/omc; HttpOnly");
    response.end('{}');
  });
  try {
    await submitCallbackToOmc(
      `${server.baseUrl}/omc`,
      "synthetic-management-key",
      "claude",
      "http://localhost:54545/callback?code=fixture-code&state=fixture-state",
      5_000,
    );
  } finally {
    await server.close();
  }

  assert.equal(requests[0]?.url, "/omc/api/auth/login");
  assert.equal(requests[0]?.origin, server.baseUrl);
  assert.deepEqual(JSON.parse(requests[0]?.body ?? ""), { password: "synthetic-management-key" });
  assert.equal(requests[1]?.url, "/omc/api/v1/management/oauth/callback");
  assert.equal(requests[1]?.origin, server.baseUrl);
  assert.equal(requests[1]?.cookie, "omc_session=fixture-session");
});

test("CPA errors redact management keys and callback authorization codes", async () => {
  const server = await startServer((_request, response) => {
    response.statusCode = 400;
    response.end("synthetic-management-key fixture-code");
  });
  try {
    await assert.rejects(
      submitCallbackToCpa(
        server.baseUrl,
        "synthetic-management-key",
        "claude",
        "http://localhost:54545/callback?code=fixture-code&state=fixture-state",
        5_000,
      ),
      (error: unknown) => error instanceof Error
        && !error.message.includes("synthetic-management-key")
        && !error.message.includes("fixture-code")
        && error.message.includes("[redacted]"),
    );
  } finally {
    await server.close();
  }
});
