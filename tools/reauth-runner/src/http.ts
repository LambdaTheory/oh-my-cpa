export interface AuthUrlResponse {
  url: string;
  state: string;
}

export interface OAuthStatusResponse {
  status: string;
  message?: string;
  error?: string;
}

interface RequestOptions {
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  timeoutMs: number;
  secrets: string[];
}

function redact(value: string, secrets: string[]): string {
  return secrets.reduce((current, secret) => secret ? current.replaceAll(secret, "[redacted]") : current, value);
}

function callbackSecrets(managementKey: string, redirectUrl: string): string[] {
  try {
    return [managementKey, redirectUrl, new URL(redirectUrl).searchParams.get("code") ?? ""];
  } catch {
    return [managementKey, redirectUrl];
  }
}

function appendPath(baseUrl: string, suffix: string): URL {
  let base: URL;
  try {
    base = new URL(baseUrl);
  } catch {
    throw new Error("base URL is invalid");
  }
  if (base.protocol !== "http:" && base.protocol !== "https:") {
    throw new Error("base URL must use http or https");
  }
  if (base.username || base.password) throw new Error("base URL must not contain user info");
  base.pathname = `${base.pathname.replace(/\/$/, "")}/${suffix.replace(/^\//, "")}`;
  base.search = "";
  base.hash = "";
  return base;
}

async function requestJson<T>(url: URL, options: RequestOptions): Promise<{ data: T; headers: Headers }> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), options.timeoutMs);
  try {
    const response = await fetch(url, {
      method: options.method ?? "GET",
      headers: { Accept: "application/json", ...options.headers },
      body: options.body,
      redirect: "manual",
      signal: controller.signal,
    });
    const body = (await response.text()).slice(0, 65_536);
    if (!response.ok) {
      throw new Error(`request returned HTTP ${response.status}${body ? `: ${redact(body, options.secrets)}` : ""}`);
    }
    if (!body) return { data: {} as T, headers: response.headers };
    try {
      return { data: JSON.parse(body) as T, headers: response.headers };
    } catch {
      throw new Error("request returned invalid JSON");
    }
  } catch (error) {
    if (error instanceof Error && error.name === "AbortError") {
      throw new Error(`request timed out after ${options.timeoutMs}ms`);
    }
    throw error;
  } finally {
    clearTimeout(timeout);
  }
}

export async function requestAuthUrl(baseUrl: string, managementKey: string, provider: string, timeoutMs: number): Promise<AuthUrlResponse> {
  const url = appendPath(baseUrl, "/v8/management/oauth/auth-url");
  url.searchParams.set("provider", provider);
  url.searchParams.set("is_webui", "true");
  const { data } = await requestJson<Partial<AuthUrlResponse>>(url, {
    timeoutMs,
    secrets: [managementKey],
    headers: { Authorization: `Bearer ${managementKey}` },
  });
  if (!data.url?.trim() || !data.state?.trim()) {
    throw new Error("CPA auth-url response must contain non-empty url and state fields");
  }
  return { url: data.url.trim(), state: data.state.trim() };
}

export async function submitCallbackToCpa(baseUrl: string, managementKey: string, provider: string, redirectUrl: string, timeoutMs: number): Promise<unknown> {
  const url = appendPath(baseUrl, "/v8/management/oauth/callback");
  const { data } = await requestJson<unknown>(url, {
    method: "POST",
    timeoutMs,
    secrets: callbackSecrets(managementKey, redirectUrl),
    headers: {
      Authorization: `Bearer ${managementKey}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ provider, redirect_url: redirectUrl }),
  });
  return data;
}

export async function submitCallbackToOmc(baseUrl: string, managementKey: string, provider: string, redirectUrl: string, timeoutMs: number): Promise<unknown> {
  const loginUrl = appendPath(baseUrl, "/api/auth/login");
  const origin = loginUrl.origin;
  const login = await requestJson<unknown>(loginUrl, {
    method: "POST",
    timeoutMs,
    secrets: callbackSecrets(managementKey, redirectUrl),
    headers: { "Content-Type": "application/json", Origin: origin },
    body: JSON.stringify({ password: managementKey }),
  });
  const cookie = login.headers.get("set-cookie")?.split(";", 1)[0]?.trim();
  if (!cookie) throw new Error("OMC login did not return a session cookie");

  const callbackUrl = appendPath(baseUrl, "/api/v1/management/oauth/callback");
  const { data } = await requestJson<unknown>(callbackUrl, {
    method: "POST",
    timeoutMs,
    secrets: [...callbackSecrets(managementKey, redirectUrl), cookie],
    headers: { "Content-Type": "application/json", Cookie: cookie, Origin: origin },
    body: JSON.stringify({ provider, redirect_url: redirectUrl }),
  });
  return data;
}

export async function getOAuthStatus(baseUrl: string, managementKey: string, state: string, timeoutMs: number): Promise<OAuthStatusResponse> {
  const url = appendPath(baseUrl, "/v8/management/oauth/status");
  url.searchParams.set("state", state);
  const { data } = await requestJson<OAuthStatusResponse>(url, {
    timeoutMs,
    secrets: [managementKey],
    headers: { Authorization: `Bearer ${managementKey}` },
  });
  return data;
}

export async function pollOAuthStatus(baseUrl: string, managementKey: string, state: string, timeoutMs: number): Promise<OAuthStatusResponse> {
  const deadline = Date.now() + timeoutMs;
  let last: OAuthStatusResponse = { status: "wait" };
  while (Date.now() < deadline) {
    last = await getOAuthStatus(baseUrl, managementKey, state, Math.min(15_000, Math.max(1_000, deadline - Date.now())));
    const status = last.status?.trim().toLowerCase();
    if (["ok", "success", "error", "failed", "failure"].includes(status)) return last;
    const remaining = deadline - Date.now();
    if (remaining <= 0) break;
    await new Promise<void>((resolve) => setTimeout(resolve, Math.min(1_000, remaining)));
  }
  throw new Error(`timed out after ${timeoutMs}ms waiting for OAuth status; last status was ${last.status || "unknown"}`);
}
