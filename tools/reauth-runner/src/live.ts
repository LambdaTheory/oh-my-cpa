import fs from "node:fs/promises";
import { createInterface } from "node:readline/promises";
import { stdin, stderr } from "node:process";
import { chromium, type BrowserContext, type Locator, type Page } from "playwright";
import type { OAuthCallback } from "./callback.js";
import { captureCallback } from "./callback.js";
import type { StartConfig } from "./config.js";
import { HttpError, getOAuthStatus, pollOAuthStatus, requestAuthUrl, submitCallbackToCpa } from "./http.js";
import { OmcSession, type ReauthJob } from "./jobs.js";
import { getProvider } from "./providers.js";
import { abortError, delay, withSignal } from "./wait.js";

interface BrowserAuthorization {
  url: string;
  state: string;
}

type BrowserAuthorizationConfig = Pick<StartConfig, "codeFile" | "isManual" | "loginEmail" | "provider" | "timeoutMs"> & {
  signal?: AbortSignal;
  readCode?: () => Promise<string>;
};

export interface LiveFlowHooks {
  onBrowserReady?: (context: BrowserContext) => Promise<void>;
  signal?: AbortSignal;
  onNotice?: (message: string) => void;
}

const CODE_SELECTORS = [
  'input[autocomplete="one-time-code"]',
  'input[name*="code" i]',
  'input[id*="code" i]',
  'input[placeholder*="code" i]',
  'input[inputmode="numeric"]',
];

const EMAIL_SELECTORS = [
  'input[type="email"]',
  'input[name*="email" i]',
  'input[id*="email" i]',
  'input[autocomplete="username"]',
  'input[placeholder*="email" i]',
];

const PASSWORD_SELECTORS = ['input[type="password"]'];

interface VisibleInputCapture {
  result: Promise<{ page: Page; locator: Locator }>;
  stop: () => void;
}

function captureVisibleInput(context: BrowserContext, selectors: string[]): VisibleInputCapture {
  let isSettled = false;
  const attachedPages = new Set<Page>();

  const stop = (): void => {
    context.off("page", attachPage);
  };

  function attachPage(page: Page): void {
    if (attachedPages.has(page)) return;
    attachedPages.add(page);
    for (const selector of selectors) {
      const locator = page.locator(`${selector}:visible`).first();
      void locator.waitFor({ state: "visible", timeout: 0 }).then(() => {
        if (isSettled) return;
        isSettled = true;
        stop();
        resolveResult({ page, locator });
      }).catch(() => undefined);
    }
  }

  let resolveResult: (value: { page: Page; locator: Locator }) => void;
  const result = new Promise<{ page: Page; locator: Locator }>((resolve) => {
    resolveResult = resolve;
  });

  context.on("page", attachPage);
  for (const page of context.pages()) attachPage(page);
  return {
    result,
    stop: () => {
      if (!isSettled) isSettled = true;
      stop();
    },
  };
}

type AutomatedStep =
  | { kind: "callback"; callback: OAuthCallback }
  | { kind: "code"; page: Page; locator: Locator }
  | { kind: "email"; page: Page; locator: Locator }
  | { kind: "password" };

async function explainAutomatedTimeout(context: BrowserContext): Promise<never> {
  const visibleText = (await Promise.all(context.pages().map((page) => page.locator("body").innerText().catch(() => "")))).join(" ").toLowerCase();
  const challenge = ["captcha", "security key", "two-factor", "2fa", "device verification"].find((term) => visibleText.includes(term));
  throw new Error(challenge
    ? `verification code input did not appear; page indicates ${challenge}`
    : "verification code input did not appear; complete any consent/challenge in the browser or retry with MANUAL=true");
}

async function waitForAutomatedStep(
  context: BrowserContext,
  callback: Promise<OAuthCallback>,
  codeInput: Promise<{ page: Page; locator: Locator }>,
  passwordInput: Promise<{ page: Page; locator: Locator }>,
  emailInput?: Promise<{ page: Page; locator: Locator }>,
): Promise<AutomatedStep> {
  const candidates: Array<Promise<AutomatedStep>> = [
    callback.then((captured) => ({ kind: "callback" as const, callback: captured })),
    codeInput.then(({ page, locator }) => ({ kind: "code" as const, page, locator })),
    passwordInput.then(() => ({ kind: "password" as const })),
  ];
  if (emailInput) {
    candidates.push(emailInput.then(({ page, locator }) => ({ kind: "email" as const, page, locator })));
  }
  try {
    return await Promise.race(candidates);
  } catch {
    return explainAutomatedTimeout(context);
  }
}

async function submitLoginEmail(page: Page, locator: Locator, loginEmail: string | undefined, timeoutMs: number): Promise<void> {
  if (!loginEmail) {
    throw new Error("LOGIN_EMAIL is required when the authorization page asks for an email address");
  }
  const continueButton = page.getByRole("button", { name: /^(continue|next|继续|下一步|sign in)$/i }).filter({ visible: true }).first();
  try {
    await locator.fill(loginEmail);
    await continueButton.waitFor({ state: "visible", timeout: timeoutMs });
    await continueButton.click({ noWaitAfter: true });
  } catch {
    throw new Error("email was entered, but a visible Continue/Next/Sign in button could not be clicked");
  }
}

function isMissingFile(error: unknown): boolean {
  return error instanceof Error && "code" in error && (error as NodeJS.ErrnoException).code === "ENOENT";
}

export async function readVerificationCode(codeFile: string | undefined, timeoutMs: number, signal?: AbortSignal): Promise<string> {
  const validate = (code: string): string => {
    if (!/^\d{6}$/.test(code)) throw new Error("verification code must contain exactly six digits");
    return code;
  };
  if (codeFile) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      signal?.throwIfAborted();
      let code = "";
      try {
        code = (await fs.readFile(codeFile, "utf8")).trim();
      } catch (error) {
        if (!isMissingFile(error)) throw new Error("CODE_FILE could not be read");
      }
      if (code) return validate(code);
      const remaining = deadline - Date.now();
      if (remaining > 0) {
        await delay(Math.min(100, remaining), signal);
      }
    }
    throw new Error(`timed out after ${timeoutMs}ms waiting for CODE_FILE to exist and contain a verification code`);
  }
  if (!stdin.isTTY) {
    let value = "";
    const onData = (chunk: string): void => { value += chunk; };
    let onEnd: () => void = () => undefined;
    try {
      stdin.setEncoding("utf8");
      stdin.on("data", onData);
      await withSignal(new Promise<void>((resolve) => {
        onEnd = resolve;
        stdin.once("end", onEnd);
        if (stdin.readableEnded) resolve();
      }), signal ?? AbortSignal.timeout(timeoutMs));
      return validate(value.trim());
    } finally {
      stdin.off("data", onData);
      stdin.off("end", onEnd);
      stdin.pause();
    }
  }
  const readline = createInterface({ input: stdin, output: stderr });
  try {
    return validate((await readline.question("Verification code: ", { signal: signal ?? AbortSignal.timeout(timeoutMs) })).trim());
  } finally {
    readline.close();
  }
}

export async function authorizeInBrowser(
  context: BrowserContext,
  auth: BrowserAuthorization,
  config: BrowserAuthorizationConfig,
): Promise<OAuthCallback> {
  const capture = captureCallback(context, config.timeoutMs, config.signal);
  const codeCapture = config.isManual ? undefined : captureVisibleInput(context, CODE_SELECTORS);
  const emailCapture = config.isManual ? undefined : captureVisibleInput(context, EMAIL_SELECTORS);
  const passwordCapture = config.isManual ? undefined : captureVisibleInput(context, PASSWORD_SELECTORS);
  try {
    const page = context.pages()[0] ?? await context.newPage();
    if (config.isManual) {
      process.stderr.write("MANUAL 模式：请在浏览器完成登录和验证码；脚本正在等待 callback。\n");
    } else {
      process.stderr.write(`OAuth started for ${config.provider}; waiting for an email field, verification-code page, or callback.\n`);
    }
    const navigation = page.goto(auth.url, { waitUntil: "domcontentloaded", timeout: config.timeoutMs });
    // A successful OAuth redirect can end in ERR_CONNECTION_REFUSED because the
    // browser's localhost is not CPA. The request listener is the success signal.
    try {
      await Promise.race([navigation, capture.result.then(() => null)]);
    } catch {
      throw new Error("authorization page failed to open");
    }

    let callback: OAuthCallback;
    if (config.isManual) {
      callback = await capture.result;
    } else {
      let next = await waitForAutomatedStep(
        context,
        capture.result,
        codeCapture!.result,
        passwordCapture!.result,
        emailCapture!.result,
      );
      if (next.kind === "email") {
        emailCapture!.stop();
        await submitLoginEmail(next.page, next.locator, config.loginEmail, config.timeoutMs);
        next = await waitForAutomatedStep(context, capture.result, codeCapture!.result, passwordCapture!.result);
      }
      emailCapture!.stop();
      passwordCapture!.stop();
      if (next.kind === "callback") {
        callback = next.callback;
      } else if (next.kind === "password") {
        throw new Error("password/SSO required; manual takeover needed; retry with MANUAL=true");
      } else {
        codeCapture!.stop();
        process.stderr.write("Waiting for a manually supplied verification code.\n");
        const code = await withSignal(config.readCode ? config.readCode() : readVerificationCode(config.codeFile, config.timeoutMs, config.signal), config.signal);
        try {
          await fillVerificationCode(next.page, next.locator, code);
        } catch {
          throw new Error("verification code could not be submitted");
        }
        callback = await capture.result;
      }
    }

    if (callback.state !== auth.state) {
      throw new Error("captured callback state does not match the CPA OAuth session");
    }
    return callback;
  } finally {
    codeCapture?.stop();
    emailCapture?.stop();
    passwordCapture?.stop();
    capture.stop();
  }
}

async function fillVerificationCode(page: Page, locator: Locator, code: string): Promise<void> {
  if (!code) throw new Error("verification code is empty");
  const maxLength = await locator.getAttribute("maxlength");
  if (maxLength === "1") {
    const inputs = page.locator(CODE_SELECTORS.map((selector) => `${selector}:visible`).join(","));
    if (await inputs.count() >= code.length) {
      for (let index = 0; index < code.length; index += 1) {
        await inputs.nth(index).fill(code[index] ?? "");
      }
    } else {
      await locator.fill(code);
    }
  } else {
    await locator.fill(code);
  }

  const submit = page.getByRole("button", { name: /verify|continue|submit|next/i }).filter({ visible: true }).first();
  if (await submit.count()) await submit.click({ noWaitAfter: true });
  else await locator.press("Enter", { noWaitAfter: true });
}

export async function runLiveFlow(config: StartConfig, hooks: LiveFlowHooks = {}): Promise<Record<string, unknown>> {
  const provider = getProvider(config.provider);
  config = { ...config, provider: provider.id };
  const notice = hooks.onNotice ?? ((message: string) => process.stderr.write(`${message}\n`));
  const controller = new AbortController();
  const signal = hooks.signal ? AbortSignal.any([controller.signal, hooks.signal]) : controller.signal;
  const timer = setTimeout(() => controller.abort(new Error("OAuth run timed out")), config.timeoutMs);
  let expiryTimer: NodeJS.Timeout | undefined;
  let context: BrowserContext | undefined;
  let omc: OmcSession | undefined;
  let job: ReauthJob | undefined;
  let hasStartedJob = false;
  let hasConsumedCode = false;
  let hasCpaSucceeded = false;
  let isClosing = false;
  let browserClose: Promise<void> | undefined;
  const closeBrowser = (): void => {
    isClosing = true;
    browserClose ??= context?.close();
    void browserClose?.catch(() => undefined);
  };
  signal.addEventListener("abort", closeBrowser, { once: true });
  try {
    signal.throwIfAborted();
    if (config.omcBaseUrl && config.omcManagementKey) {
      omc = new OmcSession(config.omcBaseUrl, config.omcManagementKey, Math.min(15_000, config.timeoutMs));
      try {
        await omc.login(signal);
        job = await omc.next(provider.id, signal);
      } catch (error) {
        if (signal.aborted) throw abortError(signal);
        // A server rejection or invalid job is not an outage and must not bypass OMC.
        if (error instanceof HttpError && error.status < 500) throw error;
        if (error instanceof Error && error.message.startsWith("OMC returned a job")) throw error;
        notice("Warning: OMC is unavailable; continuing standalone with CODE_FILE/stdin or manual login. Job progress will not be reported.");
        omc = undefined;
      }
      if (omc && !job) return { provider: provider.id, status: "idle" };
    }
    const auth = await requestAuthUrl(config.cpaBaseUrl, config.cpaManagementKey, provider.id, config.timeoutMs, signal);
    if (auth.expires_in) expiryTimer = setTimeout(() => controller.abort(new Error("device authorization expired")), auth.expires_in * 1000);
    if (omc && job) {
      await omc.report(job, "oauth_started", auth, signal);
      hasStartedJob = true;
    }
    if (provider.needsManualCallback) notice("Devin has no callback forwarder: reach CPA's own callback port using a tunnel, or paste the callback URL into the submit command in a second terminal. This start command waits for CPA status.");
    if (auth.flow === "device") notice(`Device authorization: ${auth.verification_url} | user_code: ${auth.user_code} | expires_in: ${auth.expires_in}s`);
    context = await chromium.launchPersistentContext(config.profileDir, {
      channel: config.browserChannel, headless: config.isHeadless,
      timeout: config.timeoutMs,
      ignoreDefaultArgs: ["--enable-automation"], args: ["--disable-blink-features=AutomationControlled"],
      ignoreHTTPSErrors: config.shouldIgnoreHttpsErrors, userAgent: config.userAgent,
      viewport: { width: 1280, height: 900 },
    });
    signal.throwIfAborted();
    context.on("close", () => {
      if (!isClosing) controller.abort(new Error("OAuth run cancelled: browser was closed"));
    });
    const poll = async () => {
      const current = await pollOAuthStatus(config.cpaBaseUrl, config.cpaManagementKey, auth.state, config.timeoutMs, signal);
      hasCpaSucceeded = true;
      return current;
    };
    let status;
    if (auth.flow === "device") {
      const page = context.pages()[0] ?? await context.newPage();
      await hooks.onBrowserReady?.(context);
      try { await page.goto(auth.url, { waitUntil: "domcontentloaded", timeout: config.timeoutMs }); }
      catch { throw new Error("device verification page failed to open"); }
      status = await poll();
    } else {
      const authorization = authorizeInBrowser(context, auth, {
        ...config, signal, isManual: config.isManual || provider.codeKind !== "otp",
        readCode: omc && job ? async () => {
          try {
            const code = await omc!.readCode(job!, config.timeoutMs, signal);
            hasConsumedCode = true;
            return code;
          } catch (error) {
            if (signal.aborted || error instanceof HttpError && error.status < 500) throw error;
            notice("Warning: OMC code retrieval failed; use CODE_FILE/stdin, or restart with MANUAL=true.");
            return readVerificationCode(config.codeFile, config.timeoutMs, signal);
          }
        } : undefined,
      });
      const submit = async (): Promise<void> => {
        const callback = await authorization;
        try {
          if (omc) await omc.submit(provider.id, callback.callbackUrl, signal);
          else await submitCallbackToCpa(config.cpaBaseUrl, config.cpaManagementKey, provider.id, callback.callbackUrl, config.timeoutMs, signal);
        } catch (error) {
          // A reachable CPA callback can finish the grant before our captured URL arrives.
          if (!(error instanceof HttpError) || error.status !== 409) throw error;
          const current = await getOAuthStatus(config.cpaBaseUrl, config.cpaManagementKey, auth.state, config.timeoutMs, signal);
          if (current.status !== "ok" && current.status !== "success") throw error;
          hasCpaSucceeded = true;
        }
      };
      const complete = async () => { await submit(); return poll(); };
      const completion = provider.codeKind === "otp" ? complete() : Promise.race([complete(), poll()]);
      status = hooks.onBrowserReady ? (await Promise.all([completion, hooks.onBrowserReady(context)]))[0] : await completion;
    }
    if (omc && job) await omc.reconcileSuccess(job, hasConsumedCode, signal);
    signal.throwIfAborted();
    return {
      provider: config.provider,
      state: auth.state,
      status: status.status,
    };
  } catch (error) {
    if (omc && job && hasStartedJob && !hasCpaSucceeded) {
      try { await omc.report(job, "failed"); }
      catch { notice("Warning: OMC failed-status report was not accepted; the job needs operator inspection."); }
    }
    if (signal.aborted && !hasCpaSucceeded) throw abortError(signal);
    throw error;
  } finally {
    clearTimeout(timer);
    clearTimeout(expiryTimer);
    signal.removeEventListener("abort", closeBrowser);
    isClosing = true;
    controller.abort(new Error("OAuth run finished"));
    browserClose ??= context?.close();
    await browserClose;
  }
}
