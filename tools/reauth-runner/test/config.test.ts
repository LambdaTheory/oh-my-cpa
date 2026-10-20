import assert from "node:assert/strict";
import test from "node:test";
import { loadStartConfig } from "../src/config.js";

test("reports missing CPA base URL clearly", () => {
  assert.throws(() => loadStartConfig({ CPA_MANAGEMENT_KEY: "synthetic-key" }), /CPA_BASE_URL is required/);
});

test("reports missing CPA management key clearly", () => {
  assert.throws(() => loadStartConfig({ CPA_BASE_URL: "http://cpa:8317" }), /CPA_MANAGEMENT_KEY is required/);
});

test("requires an OMC key only when the OMC path is selected", () => {
  assert.throws(() => loadStartConfig({
    CPA_BASE_URL: "http://cpa:8317",
    CPA_MANAGEMENT_KEY: "synthetic-key",
    OMC_BASE_URL: "http://omc:8080/omc",
  }), /OMC_MANAGEMENT_KEY is required/);
});

test("parses browser, MANUAL, and CODE_FILE configuration", () => {
  const config = loadStartConfig({
    CPA_BASE_URL: "http://cpa:8317",
    CPA_MANAGEMENT_KEY: "synthetic-key",
    BROWSER_CHANNEL: "  chrome  ",
    IGNORE_HTTPS_ERRORS: "yes",
    USER_AGENT: "  synthetic-browser/1.0  ",
    MANUAL: "yes",
    CODE_FILE: "  /run/code  ",
  });
  assert.equal(config.browserChannel, "chrome");
  assert.equal(config.shouldIgnoreHttpsErrors, true);
  assert.equal(config.userAgent, "synthetic-browser/1.0");
  assert.equal(config.isManual, true);
  assert.equal(config.codeFile, "/run/code");
  const defaults = loadStartConfig({
    CPA_BASE_URL: "http://cpa:8317",
    CPA_MANAGEMENT_KEY: "synthetic-key",
    BROWSER_CHANNEL: " ",
    USER_AGENT: " ",
  });
  assert.equal(defaults.browserChannel, undefined);
  assert.equal(defaults.shouldIgnoreHttpsErrors, false);
  assert.equal(defaults.userAgent, undefined);
  assert.equal(defaults.isManual, false);
  assert.throws(() => loadStartConfig({
    CPA_BASE_URL: "http://cpa:8317",
    CPA_MANAGEMENT_KEY: "synthetic-key",
    MANUAL: "sometimes",
  }), /MANUAL must be true\/false/);
  assert.throws(() => loadStartConfig({
    CPA_BASE_URL: "http://cpa:8317",
    CPA_MANAGEMENT_KEY: "synthetic-key",
    IGNORE_HTTPS_ERRORS: "sometimes",
  }), /IGNORE_HTTPS_ERRORS must be true\/false/);
});
