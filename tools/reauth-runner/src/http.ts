import { getProvider, normalizeVerificationUrl } from "./providers.js";
import { abortError, delay } from "./wait.js";

export interface AuthUrlResponse {
  url: string;
  state: string;
  flow?: "device";
  user_code?: string;
  verification_url?: string;
  expires_in?: number;
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
  signal?: AbortSignal;
}

export function appendPath(baseUrl: string, suffix: string): URL {
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

export class HttpError extends Error {
  constructor(readonly status: number) { super(`request returned HTTP ${status}; response body [redacted]`); }
}

export async function requestJson<T>(url: URL, options: RequestOptions): Promise<{ data: T; headers: Headers; status: number }> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), options.timeoutMs);
  try {
    const response = await fetch(url, {
      method: options.method ?? "GET",
      headers: { Accept: "application/json", ...options.headers },
      body: options.body,
      redirect: "manual",
      signal: options.signal ? AbortSignal.any([controller.signal, options.signal]) : controller.signal,
    });
    const body = (await response.text()).slice(0, 65_536);
    if (!response.ok) {
      throw new HttpError(response.status);
    }
    if (!body) return { data: {} as T, headers: response.headers, status: response.status };
    try {
      return { data: JSON.parse(body) as T, headers: response.headers, status: response.status };
    } catch {
      throw new Error("request returned invalid JSON");
    }
  } catch (error) {
    if (options.signal?.aborted) throw abortError(options.signal);
    if (error instanceof Error && error.name === "AbortError") {
      throw new Error(`request timed out after ${options.timeoutMs}ms`);
    }
    if (error instanceof HttpError) throw error;
    if (error instanceof Error && error.message === "request returned invalid JSON") throw error;
    throw new Error("request failed; check endpoint reachability");
  } finally {
    clearTimeout(timeout);
  }
}

export async function requestAuthUrl(baseUrl: string, managementKey: string, provider: string, timeoutMs: number, signal?: AbortSignal): Promise<AuthUrlResponse> {
  const metadata = getProvider(provider);
  const url = appendPath(baseUrl, "/v8/management/oauth/auth-url");
  url.searchParams.set("provider", metadata.id);
  if (metadata.callbackPort) url.searchParams.set("is_webui", "true");
  const { data } = await requestJson<Partial<AuthUrlResponse>>(url, {
    timeoutMs,
    signal,
    headers: { Authorization: `Bearer ${managementKey}` },
  });
  if (typeof data.url !== "string" || !data.url.trim() || typeof data.state !== "string" || !data.state.trim()) {
    throw new Error("CPA auth-url response must contain non-empty url and state fields");
  }
  if (metadata.codeKind === "device") {
    if (data.flow !== "device" || typeof data.user_code !== "string" || !/^[A-Za-z0-9][A-Za-z0-9 -]{0,63}$/.test(data.user_code)
      || !Number.isSafeInteger(data.expires_in) || data.expires_in! <= 0 || data.expires_in! > 86400) {
      throw new Error("CPA device response requires flow=device, user_code, and expires_in between 1 and 86400");
    }
    const verificationUrl = normalizeVerificationUrl(data.verification_url ?? data.url);
    return { url: verificationUrl, state: data.state.trim(), flow: "device", user_code: data.user_code, verification_url: verificationUrl, expires_in: data.expires_in };
  }
  if (data.flow !== undefined && (data.flow as string) !== "redirect") throw new Error("CPA flow does not match the selected provider");
  return { url: data.url.trim(), state: data.state.trim() };
}

export async function submitCallbackToCpa(baseUrl: string, managementKey: string, provider: string, redirectUrl: string, timeoutMs: number, signal?: AbortSignal): Promise<unknown> {
  const metadata = getProvider(provider);
  if (metadata.codeKind === "device") throw new Error("device providers do not accept callback submission");
  const url = appendPath(baseUrl, "/v8/management/oauth/callback");
  const { data } = await requestJson<unknown>(url, {
    method: "POST",
    timeoutMs,
    signal,
    headers: {
      Authorization: `Bearer ${managementKey}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ provider: metadata.id, redirect_url: redirectUrl }),
  });
  return data;
}

export async function submitCallbackToOmc(baseUrl: string, managementKey: string, provider: string, redirectUrl: string, timeoutMs: number): Promise<unknown> {
  const metadata = getProvider(provider);
  if (metadata.codeKind === "device") throw new Error("device providers do not accept callback submission");
  const loginUrl = appendPath(baseUrl, "/api/auth/login");
  const origin = loginUrl.origin;
  const login = await requestJson<unknown>(loginUrl, {
    method: "POST",
    timeoutMs,
    headers: { "Content-Type": "application/json", Origin: origin },
    body: JSON.stringify({ password: managementKey }),
  });
  const cookie = login.headers.get("set-cookie")?.split(";", 1)[0]?.trim();
  if (!cookie) throw new Error("OMC login did not return a session cookie");

  const callbackUrl = appendPath(baseUrl, "/api/v1/management/oauth/callback");
  const { data } = await requestJson<unknown>(callbackUrl, {
    method: "POST",
    timeoutMs,
    headers: { "Content-Type": "application/json", Cookie: cookie, Origin: origin },
    body: JSON.stringify({ provider: metadata.id, redirect_url: redirectUrl }),
  });
  return data;
}

export async function getOAuthStatus(baseUrl: string, managementKey: string, state: string, timeoutMs: number, signal?: AbortSignal): Promise<OAuthStatusResponse> {
  const url = appendPath(baseUrl, "/v8/management/oauth/status");
  url.searchParams.set("state", state);
  url.searchParams.set("session_id", state);
  const { data } = await requestJson<OAuthStatusResponse>(url, {
    timeoutMs,
    signal,
    headers: { Authorization: `Bearer ${managementKey}` },
  });
  const status = typeof data.status === "string" ? data.status.trim().toLowerCase() : "";
  if (!["ok", "success", "wait", "pending", "error", "failed", "failure", "cancelled", "canceled", "expired"].includes(status)) {
    throw new Error("CPA returned an unknown OAuth status");
  }
  // Upstream messages can echo codes or credentials, so only the status is public.
  return { status };
}

export async function pollOAuthStatus(baseUrl: string, managementKey: string, state: string, timeoutMs: number, signal?: AbortSignal): Promise<OAuthStatusResponse> {
  const deadline = Date.now() + timeoutMs;
  let last: OAuthStatusResponse = { status: "wait" };
  while (Date.now() < deadline) {
    last = await getOAuthStatus(baseUrl, managementKey, state, Math.min(15_000, Math.max(1, deadline - Date.now())), signal);
    const status = last.status?.trim().toLowerCase();
    if (["ok", "success"].includes(status)) return last;
    if (status !== "wait" && status !== "pending") throw new Error(`OAuth ended with status ${status}`);
    const remaining = deadline - Date.now();
    if (remaining <= 0) break;
    await delay(Math.min(1_000, remaining), signal);
  }
  throw new Error(`timed out after ${timeoutMs}ms waiting for OAuth status; last status was ${last.status || "unknown"}`);
}
