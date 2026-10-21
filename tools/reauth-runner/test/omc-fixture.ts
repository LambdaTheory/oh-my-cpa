import type { ReauthJob } from "../src/jobs.js";
import { getProvider } from "../src/providers.js";

// Mirrors Service.Report/AcceptCode/TakeCode at OMC 469a30a. Time and quota
// verification advance explicitly so tests never depend on background sleeps.
export class OmcFixture {
  readonly job: ReauthJob;
  readonly states = ["pending"];
  hasConsumedCode = false;
  private code: string | undefined;
  private hasExpired = false;

  constructor(providerId: string) {
    const provider = getProvider(providerId);
    this.job = { id: "job-fixture", provider: provider.id, flow: provider.flow, code_kind: provider.codeKind, state: "pending" };
  }

  private transition(state: string): void {
    this.job.state = state;
    this.states.push(state);
  }

  advanceWaiting(): void {
    if (this.job.state !== "oauth_started") throw new Error("fixture must start OAuth before waiting");
    const waiting = { redirect_otp: "waiting_code", redirect_browser: "waiting_browser",
      redirect_manual_callback: "waiting_callback", device_code: "waiting_device" };
    this.transition(waiting[this.job.flow as keyof typeof waiting]);
  }

  acceptCode(): void {
    if (this.job.code_kind !== "otp" || this.job.state !== "waiting_code" || this.hasExpired) return;
    this.code = "123456";
    this.transition("code_received");
  }

  expireCode(): void { this.hasExpired = true; }

  takeCode(): string | undefined {
    if (this.job.code_kind !== "otp" || this.job.state !== "code_received" || this.hasConsumedCode) return;
    if (!this.code || this.hasExpired) {
      this.code = undefined;
      this.transition("expired");
      return;
    }
    const code = this.code;
    this.code = undefined;
    this.hasConsumedCode = true;
    return code;
  }

  report(body: Record<string, unknown>): boolean {
    const state = this.job.state;
    const active = ["oauth_started", "waiting_browser", "waiting_callback", "waiting_device", "waiting_code", "code_received", "oauth_submitted"];
    if (active.includes(state) && this.hasExpired) return false;
    if (body.status !== "oauth_started" && ["flow", "user_code", "verification_url", "expires_in"].some((field) => body[field] !== undefined)) return false;
    switch (body.status) {
      case "oauth_started":
        if (body.flow !== this.job.flow) return false;
        if (this.job.code_kind === "device" && (!body.user_code || !body.verification_url || !Number.isInteger(body.expires_in))) return false;
        if (state !== "pending") return false;
        this.transition("oauth_started");
        return true;
      case "code_submitted":
        if (state === "oauth_submitted") return true;
        if (this.job.code_kind !== "otp" || state !== "code_received" || !this.hasConsumedCode) return false;
        this.transition("oauth_submitted");
        return true;
      case "succeeded":
        if (state === "verifying" || state === "succeeded") return true;
        if (!["oauth_started", "waiting_code", "waiting_browser", "waiting_callback", "waiting_device", "oauth_submitted"].includes(state)) return false;
        this.transition("verifying");
        return true;
      case "failed":
        if (body.last_error !== "runner_failed") return false;
        if (state === "failed") return true;
        if (["pending", "succeeded", "expired", "verifying"].includes(state)) return false;
        this.transition("failed");
        this.code = undefined;
        return true;
      default: return false;
    }
  }

  verifyHealthy(): void {
    if (this.job.state !== "verifying") throw new Error("quota verification requires verifying state");
    this.transition("succeeded");
  }
}
