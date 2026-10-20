#!/usr/bin/env node
import { stdin } from "node:process";
import { parseCallbackUrl } from "./callback.js";
import { loadCpaConfig, loadStartConfig } from "./config.js";
import { runFixtureFlow } from "./fixture.js";
import { getOAuthStatus, pollOAuthStatus, submitCallbackToCpa, submitCallbackToOmc } from "./http.js";
import { runLiveFlow } from "./live.js";

function printJson(value: unknown): void {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

async function readStdin(): Promise<string> {
  let value = "";
  stdin.setEncoding("utf8");
  for await (const chunk of stdin) value += chunk;
  return value.trim();
}

async function runSubmit(): Promise<void> {
  const config = loadCpaConfig(process.env);
  const redirectUrl = process.argv[3]?.trim() || await readStdin();
  if (!redirectUrl) throw new Error("callback URL is required as stdin or the first argument");
  parseCallbackUrl(redirectUrl);
  if (process.env.OMC_BASE_URL?.trim()) {
    const omcKey = process.env.OMC_MANAGEMENT_KEY?.trim();
    if (!omcKey) throw new Error("OMC_MANAGEMENT_KEY is required when OMC_BASE_URL is configured");
    await submitCallbackToOmc(process.env.OMC_BASE_URL.trim(), omcKey, config.provider, redirectUrl, config.timeoutMs);
  } else {
    await submitCallbackToCpa(config.cpaBaseUrl, config.cpaManagementKey, config.provider, redirectUrl, config.timeoutMs);
  }
  printJson({ provider: config.provider, state: parseCallbackUrl(redirectUrl).state, submitted: true });
}

async function runStatus(): Promise<void> {
  const config = loadCpaConfig(process.env);
  const state = process.argv[3]?.trim();
  if (!state) throw new Error("OAuth state is required as the first argument");
  const shouldPoll = process.argv.includes("--once") === false;
  const status = shouldPoll
    ? await pollOAuthStatus(config.cpaBaseUrl, config.cpaManagementKey, state, config.timeoutMs)
    : await getOAuthStatus(config.cpaBaseUrl, config.cpaManagementKey, state, config.timeoutMs);
  printJson(status);
}

async function main(): Promise<void> {
  const command = process.argv[2];
  switch (command) {
    case "fixture":
      printJson(await runFixtureFlow());
      return;
    case "start":
      printJson(await runLiveFlow(loadStartConfig(process.env)));
      return;
    case "submit":
      await runSubmit();
      return;
    case "status":
      await runStatus();
      return;
    default:
      throw new Error("usage: cli.ts <fixture|start|submit|status>");
  }
}

main().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : "unknown error";
  process.stderr.write(`error: ${message}\n`);
  process.exitCode = 1;
});
