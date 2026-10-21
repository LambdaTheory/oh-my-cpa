import { appendPath, requestJson, type AuthUrlResponse } from "./http.js";
import { getProvider } from "./providers.js";
import { delay } from "./wait.js";

export interface ReauthJob {
  id: string;
  provider: string;
  flow: string;
  code_kind: string;
  state: string;
}

export class OmcSession {
  private cookie = "";
  constructor(private readonly baseUrl: string, private readonly key: string, private readonly timeoutMs: number) {}

  async login(signal?: AbortSignal): Promise<void> {
    const url = appendPath(this.baseUrl, "/api/auth/login");
    const result = await requestJson<unknown>(url, {
      method: "POST", timeoutMs: this.timeoutMs, signal,
      headers: { "Content-Type": "application/json", Origin: url.origin },
      body: JSON.stringify({ password: this.key }),
    });
    this.cookie = result.headers.getSetCookie().find((cookie) => cookie.startsWith("omc_session="))?.split(";", 1)[0]?.trim() ?? "";
    if (!this.cookie) throw new Error("OMC login did not return a session cookie");
  }

  async request<T>(path: string, body?: unknown, signal?: AbortSignal, query?: Record<string, string>): Promise<T | undefined> {
    const url = appendPath(this.baseUrl, `/api/v1/management/${path}`);
    for (const [key, value] of Object.entries(query ?? {})) url.searchParams.set(key, value);
    const result = await requestJson<T>(url, {
      method: body === undefined ? "GET" : "POST", timeoutMs: this.timeoutMs, signal,
      headers: { "Content-Type": "application/json", Cookie: this.cookie, Origin: url.origin },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    return result.status === 204 ? undefined : result.data;
  }

  async next(provider: string, signal?: AbortSignal): Promise<ReauthJob | undefined> {
    const metadata = getProvider(provider);
    const job = await this.request<ReauthJob>("reauth/jobs/next", undefined, signal, { provider: metadata.id });
    if (job && (typeof job.id !== "string" || !job.id || job.provider !== metadata.id
      || job.flow !== metadata.flow || job.code_kind !== metadata.codeKind || job.state !== "pending")) {
      throw new Error("OMC returned a job that does not match the requested provider or pending state");
    }
    return job;
  }

  async report(job: ReauthJob, status: "oauth_started" | "code_submitted" | "succeeded" | "failed", auth?: AuthUrlResponse, signal?: AbortSignal): Promise<void> {
    const start = status === "oauth_started" ? {
      flow: job.flow,
      ...(auth?.flow === "device" ? { user_code: auth.user_code, verification_url: auth.verification_url, expires_in: auth.expires_in } : {}),
    } : {};
    await this.request(`reauth/jobs/${encodeURIComponent(job.id)}/status`, {
      status, ...start, ...(status === "failed" ? { last_error: "runner_failed" } : {}),
    }, signal);
  }

  async readCode(job: ReauthJob, timeoutMs: number, signal?: AbortSignal): Promise<string> {
    if (getProvider(job.provider).codeKind !== "otp") throw new Error("only OTP jobs can read a verification code");
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const result = await this.request<{ code: string }>(`reauth/jobs/${encodeURIComponent(job.id)}/code`, undefined, signal);
      if (result) {
        if (typeof result.code !== "string" || !/^\d{6}$/.test(result.code)) throw new Error("OMC returned an invalid six-digit verification code");
        return result.code;
      }
      await delay(Math.min(500, Math.max(0, deadline - Date.now())), signal);
    }
    throw new Error("timed out waiting for the OMC verification code");
  }

  async submit(provider: string, redirectUrl: string, signal?: AbortSignal): Promise<void> {
    const metadata = getProvider(provider);
    if (metadata.codeKind === "device") throw new Error("device providers do not accept callback submission");
    await this.request("oauth/callback", { provider: metadata.id, redirect_url: redirectUrl }, signal);
  }
}
