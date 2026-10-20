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
