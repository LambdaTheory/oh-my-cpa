import os from "node:os";
import path from "node:path";

export interface StartConfig {
  cpaBaseUrl: string;
  cpaManagementKey: string;
  provider: string;
  isHeadless: boolean;
  isManual: boolean;
  profileDir: string;
  codeFile?: string;
  timeoutMs: number;
  omcBaseUrl?: string;
  omcManagementKey?: string;
}

function requireValue(environment: NodeJS.ProcessEnv, name: string): string {
  const value = environment[name]?.trim() ?? "";
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function parseBoolean(value: string | undefined, fallback: boolean, name: string): boolean {
  if (value === undefined || value.trim() === "") return fallback;
  switch (value.trim().toLowerCase()) {
    case "1":
    case "true":
    case "yes":
      return true;
    case "0":
    case "false":
    case "no":
      return false;
    default:
      throw new Error(`${name} must be true/false, yes/no, or 1/0`);
  }
}

function parseTimeout(value: string | undefined): number {
  if (value === undefined || value.trim() === "") return 180_000;
  const timeoutMs = Number(value);
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1_000) {
    throw new Error("TIMEOUT_MS must be an integer of at least 1000");
  }
  return timeoutMs;
}

export function loadStartConfig(environment: NodeJS.ProcessEnv): StartConfig {
  const omcBaseUrl = environment.OMC_BASE_URL?.trim() || undefined;
  const omcManagementKey = environment.OMC_MANAGEMENT_KEY?.trim() || undefined;
  if (omcBaseUrl && !omcManagementKey) {
    throw new Error("OMC_MANAGEMENT_KEY is required when OMC_BASE_URL is configured");
  }

  return {
    cpaBaseUrl: requireValue(environment, "CPA_BASE_URL"),
    cpaManagementKey: requireValue(environment, "CPA_MANAGEMENT_KEY"),
    provider: environment.PROVIDER?.trim().toLowerCase() || "claude",
    isHeadless: parseBoolean(environment.HEADLESS, true, "HEADLESS"),
    isManual: parseBoolean(environment.MANUAL, false, "MANUAL"),
    profileDir: environment.PROFILE_DIR?.trim() || path.join(os.tmpdir(), "omc-reauth-runner-profile"),
    codeFile: environment.CODE_FILE?.trim() || undefined,
    timeoutMs: parseTimeout(environment.TIMEOUT_MS),
    omcBaseUrl,
    omcManagementKey,
  };
}

export function loadCpaConfig(environment: NodeJS.ProcessEnv): Pick<StartConfig, "cpaBaseUrl" | "cpaManagementKey" | "provider" | "timeoutMs"> {
  return {
    cpaBaseUrl: requireValue(environment, "CPA_BASE_URL"),
    cpaManagementKey: requireValue(environment, "CPA_MANAGEMENT_KEY"),
    provider: environment.PROVIDER?.trim().toLowerCase() || "claude",
    timeoutMs: parseTimeout(environment.TIMEOUT_MS),
  };
}
