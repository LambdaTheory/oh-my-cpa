export function abortError(signal: AbortSignal): Error {
  return signal.reason instanceof Error ? signal.reason : new Error("OAuth run cancelled");
}

export async function withSignal<T>(promise: Promise<T>, signal?: AbortSignal): Promise<T> {
  if (!signal) return promise;
  let onAbort: () => void = () => undefined;
  const aborted = new Promise<never>((_resolve, reject) => {
    onAbort = () => reject(abortError(signal));
    signal.addEventListener("abort", onAbort, { once: true });
    if (signal.aborted) onAbort();
  });
  try { return await Promise.race([promise, aborted]); }
  finally { signal.removeEventListener("abort", onAbort); }
}

export async function delay(ms: number, signal?: AbortSignal): Promise<void> {
  let timer: NodeJS.Timeout | undefined;
  try { await withSignal(new Promise<void>((resolve) => { timer = setTimeout(resolve, ms); }), signal); }
  finally { clearTimeout(timer); }
}
