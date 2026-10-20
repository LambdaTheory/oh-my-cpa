import type { BrowserContext, Frame, Page, Request } from "playwright";

export interface OAuthCallback {
  callbackUrl: string;
  code: string;
  state: string;
}

export function parseCallbackUrl(value: string): OAuthCallback {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error("callback URL is invalid");
  }

  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("callback URL must use http or https");
  }
  const code = parsed.searchParams.get("code")?.trim() ?? "";
  const state = parsed.searchParams.get("state")?.trim() ?? "";
  if (!code || !state) {
    throw new Error("callback URL must contain non-empty code and state parameters");
  }
  return { callbackUrl: parsed.toString(), code, state };
}

export interface CallbackCapture {
  result: Promise<OAuthCallback>;
  stop: () => void;
}

export function captureCallback(context: BrowserContext, timeoutMs: number): CallbackCapture {
  let isSettled = false;
  let timeout: NodeJS.Timeout | undefined;
  const pageListeners = new Map<Page, { request: (request: Request) => void; frame: (frame: Frame) => void }>();
  let resolveResult: (value: OAuthCallback) => void;
  let rejectResult: (reason: Error) => void;

  const result = new Promise<OAuthCallback>((resolve, reject) => {
    resolveResult = resolve;
    rejectResult = reject;
  });

  const stop = (): void => {
    if (timeout) {
      clearTimeout(timeout);
      timeout = undefined;
    }
    context.off("page", onPage);
    for (const [page, listeners] of pageListeners) {
      page.off("request", listeners.request);
      page.off("framenavigated", listeners.frame);
    }
    pageListeners.clear();
  };

  const inspect = (value: string): void => {
    if (isSettled) return;
    try {
      const callback = parseCallbackUrl(value);
      isSettled = true;
      stop();
      resolveResult(callback);
    } catch {
      // Most browser events are unrelated navigations; only OAuth callbacks match.
    }
  };

  const attachPage = (page: Page): void => {
    if (pageListeners.has(page)) return;
    const request = (event: Request): void => inspect(event.url());
    const frame = (event: Frame): void => inspect(event.url());
    page.on("request", request);
    page.on("framenavigated", frame);
    pageListeners.set(page, { request, frame });
    inspect(page.url());
  };

  const onPage = (page: Page): void => attachPage(page);
  context.on("page", onPage);
  for (const page of context.pages()) attachPage(page);

  timeout = setTimeout(() => {
    if (isSettled) return;
    isSettled = true;
    stop();
    rejectResult(new Error(`timed out after ${timeoutMs}ms waiting for an OAuth callback URL`));
  }, timeoutMs);

  return { result, stop };
}
