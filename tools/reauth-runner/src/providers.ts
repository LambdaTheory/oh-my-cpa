export type ProviderFlow = "redirect_otp" | "redirect_browser" | "redirect_manual_callback" | "device_code";

export interface Provider {
  id: string;
  aliases: readonly string[];
  flow: ProviderFlow;
  codeKind: "otp" | "none" | "device";
  callbackPort?: number;
  needsManualCallback: boolean;
}

export const PROVIDERS: readonly Provider[] = [
  { id: "claude", aliases: ["anthropic"], flow: "redirect_otp", codeKind: "otp", callbackPort: 54545, needsManualCallback: false },
  { id: "codex", aliases: ["openai"], flow: "redirect_otp", codeKind: "otp", callbackPort: 1455, needsManualCallback: false },
  { id: "antigravity", aliases: [], flow: "redirect_browser", codeKind: "none", callbackPort: 51121, needsManualCallback: false },
  { id: "devin", aliases: [], flow: "redirect_manual_callback", codeKind: "none", needsManualCallback: true },
  ...["kimi", "kimi-ai", "xai", "meta"].map((id): Provider => ({
    id, aliases: id === "xai" ? ["grok"] : [], flow: "device_code", codeKind: "device", needsManualCallback: false,
  })),
];

export function getProvider(value: string): Provider {
  const id = value.trim().toLowerCase();
  if (["gemini", "vertex", "api-keys"].includes(id) || id.startsWith("api-keys.")) {
    throw new Error("provider is not OAuth; replace its API key or service-account credential separately");
  }
  const provider = PROVIDERS.find((entry) => entry.id === id || entry.aliases.includes(id));
  if (!provider) throw new Error(`unknown OAuth provider; supported IDs: ${PROVIDERS.map((entry) => entry.id).join(", ")}`);
  return provider;
}

export function normalizeVerificationUrl(value: string): string {
  let url: URL;
  try { url = new URL(value); } catch { throw new Error("device verification URL is invalid"); }
  if (url.protocol !== "https:" || url.username || url.password || url.port || !url.hostname
    || /[<>"\s]/.test(value) || value.length > 512 || url.hash) {
    throw new Error("device verification URL must be HTTPS without credentials, port, fragment, or markup");
  }
  // CPA can return a prefilled link. Never display or forward its query data.
  url.search = "";
  return url.toString();
}
