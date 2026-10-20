import fs from "node:fs/promises";
import { createInterface } from "node:readline/promises";
import { stdin, stderr } from "node:process";
import { chromium, type BrowserContext, type Locator, type Page } from "playwright";
import type { OAuthCallback } from "./callback.js";
import { captureCallback } from "./callback.js";
import type { StartConfig } from "./config.js";
import { pollOAuthStatus, requestAuthUrl, submitCallbackToCpa, submitCallbackToOmc } from "./http.js";

interface BrowserAuthorization {
  url: string;
  state: string;
}

type BrowserAuthorizationConfig = Pick<StartConfig, "codeFile" | "isManual" | "provider" | "timeoutMs">;

export interface LiveFlowHooks {
  onBrowserReady?: (context: BrowserContext) => Promise<void>;
}

const CODE_SELECTORS = [
  'input[autocomplete="one-time-code"]',
  'input[name*="code" i]',
  'input[id*="code" i]',
  'input[placeholder*="code" i]',
  'input[inputmode="numeric"]',
];

interface CodeInputCapture {
  result: Promise<{ page: Page; locator: Locator }>;
  stop: () => void;
}

function captureCodeInput(context: BrowserContext, timeoutMs: number): CodeInputCapture {
  let isSettled = false;
  let timeout: NodeJS.Timeout | undefined;
  const attachedPages = new Set<Page>();
  let rejectResult: (reason: Error) => void;

  const stop = (): void => {
    if (timeout) {
      clearTimeout(timeout);
      timeout = undefined;
    }
    context.off("page", attachPage);
  };

  function attachPage(page: Page): void {
    if (attachedPages.has(page)) return;
    attachedPages.add(page);
    for (const selector of CODE_SELECTORS) {
      const locator = page.locator(`${selector}:visible`).first();
      void locator.waitFor({ state: "visible", timeout: timeoutMs }).then(() => {
        if (isSettled) return;
        isSettled = true;
        stop();
        resolveResult({ page, locator });
      }).catch(() => undefined);
    }
  }

  let resolveResult: (value: { page: Page; locator: Locator }) => void;
  const result = new Promise<{ page: Page; locator: Locator }>((resolve, reject) => {
    resolveResult = resolve;
    rejectResult = reject;
  });

  timeout = setTimeout(async () => {
    if (isSettled) return;
    isSettled = true;
    stop();
    const visibleText = (await Promise.all(context.pages().map((page) => page.locator("body").innerText().catch(() => "")))).join(" ").toLowerCase();
    const challenge = ["captcha", "security key", "two-factor", "2fa", "device verification"].find((term) => visibleText.includes(term));
    rejectResult(new Error(challenge
      ? `verification code input did not appear; page indicates ${challenge}`
      : "verification code input did not appear; enter the account email or complete any consent/challenge in the browser"));
  }, timeoutMs);

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

function isMissingFile(error: unknown): boolean {
  return error instanceof Error && "code" in error && (error as NodeJS.ErrnoException).code === "ENOENT";
}

export async function readVerificationCode(codeFile: string | undefined, timeoutMs: number): Promise<string> {
  if (codeFile) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      try {
        const code = (await fs.readFile(codeFile, "utf8")).trim();
        if (code) return code;
      } catch (error) {
        if (!isMissingFile(error)) throw new Error("CODE_FILE could not be read");
      }
      const remaining = deadline - Date.now();
      if (remaining > 0) {
        await new Promise<void>((resolve) => setTimeout(resolve, Math.min(100, remaining)));
      }
    }
    throw new Error(`timed out after ${timeoutMs}ms waiting for CODE_FILE to exist and contain a verification code`);
  }
  if (!stdin.isTTY) {
    let value = "";
    stdin.setEncoding("utf8");
    for await (const chunk of stdin) value += chunk;
    const code = value.trim();
    if (!code) throw new Error("stdin did not contain a verification code");
    return code;
  }
  const readline = createInterface({ input: stdin, output: stderr });
  try {
    return (await readline.question("Verification code: ")).trim();
  } finally {
    readline.close();
  }
}

export async function authorizeInBrowser(
  context: BrowserContext,
  auth: BrowserAuthorization,
  config: BrowserAuthorizationConfig,
): Promise<OAuthCallback> {
  const capture = captureCallback(context, config.timeoutMs);
  const codeCapture = config.isManual ? undefined : captureCodeInput(context, config.timeoutMs);
  try {
    const page = context.pages()[0] ?? await context.newPage();
    if (config.isManual) {
      process.stderr.write("MANUAL 模式：请在浏览器完成登录和验证码；脚本正在等待 callback。\n");
    } else {
      process.stderr.write(`OAuth started for ${config.provider}; waiting for the verification-code page or callback.\n`);
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
      const next = await Promise.race([
        capture.result.then((captured) => ({ kind: "callback" as const, callback: captured })),
        codeCapture!.result.then(({ page: codePage, locator }) => ({ kind: "code" as const, page: codePage, locator })),
      ]);
      codeCapture!.stop();
      if (next.kind === "callback") {
        callback = next.callback;
      } else {
        process.stderr.write("Waiting for a manually supplied verification code.\n");
        const code = await readVerificationCode(config.codeFile, config.timeoutMs);
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

async function submitCapturedCallback(config: StartConfig, callback: OAuthCallback): Promise<void> {
  if (config.omcBaseUrl && config.omcManagementKey) {
    await submitCallbackToOmc(config.omcBaseUrl, config.omcManagementKey, config.provider, callback.callbackUrl, config.timeoutMs);
    return;
  }
  await submitCallbackToCpa(config.cpaBaseUrl, config.cpaManagementKey, config.provider, callback.callbackUrl, config.timeoutMs);
}

export async function runLiveFlow(config: StartConfig, hooks: LiveFlowHooks = {}): Promise<Record<string, unknown>> {
  const auth = await requestAuthUrl(config.cpaBaseUrl, config.cpaManagementKey, config.provider, config.timeoutMs);
  const context = await chromium.launchPersistentContext(config.profileDir, {
    headless: config.isHeadless,
    viewport: { width: 1280, height: 900 },
  });
  try {
    const authorization = authorizeInBrowser(context, auth, config);
    const callback = hooks.onBrowserReady
      ? (await Promise.all([authorization, hooks.onBrowserReady(context)]))[0]
      : await authorization;
    await submitCapturedCallback(config, callback);
    const status = await pollOAuthStatus(config.cpaBaseUrl, config.cpaManagementKey, auth.state, config.timeoutMs);
    return {
      provider: config.provider,
      state: auth.state,
      status: status.status,
      message: status.message ?? status.error ?? "",
    };
  } finally {
    await context.close();
  }
}
