import { HttpError, appendPath, requestJson, type AuthUrlResponse } from "./http.js";
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

  async reconcileSuccess(job: ReauthJob, hasConsumedCode: boolean, signal?: AbortSignal): Promise<void> {
    let state = "unknown";
    let operation = "read job";
    const readState = async (): Promise<string> => {
      operation = "read job";
      const current = await this.request<ReauthJob>(`reauth/jobs/${encodeURIComponent(job.id)}`, undefined, signal);
      const states = ["pending", "oauth_started", "waiting_code", "waiting_browser", "waiting_callback",
        "waiting_device", "code_received", "oauth_submitted", "verifying", "succeeded", "failed", "expired"];
      if (!current || current.id !== job.id || current.provider !== job.provider || current.flow !== job.flow
        || current.code_kind !== job.code_kind || !states.includes(current.state)) {
        throw new Error("OMC returned an invalid reconciliation job");
      }
      state = current.state;
      return state;
    };
    const settleCode = async (): Promise<void> => {
      if (state !== "code_received") return;
      if (job.code_kind !== "otp") throw new Error("only OTP jobs can reconcile code_received");
      if (!hasConsumedCode) {
        operation = "TakeCode";
        // Manual/file input already completed at CPA. Consume the pending OMC
        // code only to settle its one-use lifecycle; never log or resubmit it.
        await this.request(`reauth/jobs/${encodeURIComponent(job.id)}/code`, undefined, signal);
        await readState();
        if (state !== "code_received") throw new Error("OMC code is no longer available for settlement");
      }
      // A 204 can also mean an earlier TakeCode committed but lost its response.
      // Report enforces CodeConsumed server-side; the DTO does not expose it.
      operation = "Report(code_submitted)";
      await this.report(job, "code_submitted", undefined, signal);
      state = "oauth_submitted";
    };
    try {
      await readState();
      await settleCode();
      operation = "Report(succeeded)";
      try {
        await this.report(job, "succeeded", undefined, signal);
      } catch (error) {
        if (!(error instanceof HttpError) || error.status !== 409) throw error;
        const previous = state;
        await readState();
        if (state === "verifying" || state === "succeeded") return;
        // AcceptCode can race the read above. Reconcile that observed transition
        // once; an unchanged conflict is not permission to retry or mark failed.
        if (!["oauth_started", "waiting_code"].includes(previous) || state !== "code_received") {
          operation = "Report(succeeded)";
          throw error;
        }
        await settleCode();
        operation = "Report(succeeded)";
        await this.report(job, "succeeded", undefined, signal);
      }
    } catch (error) {
      const reason = error instanceof HttpError ? `HTTP ${error.status}` : error instanceof Error ? error.message : "request failed";
      throw new Error(`CPA OAuth succeeded, but OMC reconciliation failed at ${operation} (state=${state}): ${reason}; inspect the job before rerunning OAuth`);
    }
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
