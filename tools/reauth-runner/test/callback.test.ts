import assert from "node:assert/strict";
import test from "node:test";
import { parseCallbackUrl } from "../src/callback.js";

test("parses callback code and state", () => {
  assert.deepEqual(parseCallbackUrl("http://localhost:54545/callback?code=fixture-code&state=fixture-state"), {
    callbackUrl: "http://localhost:54545/callback?code=fixture-code&state=fixture-state",
    code: "fixture-code",
    state: "fixture-state",
  });
});

for (const [name, value] of [
  ["invalid URL", "not a url"],
  ["unsupported scheme", "file:///callback?code=a&state=b"],
  ["missing code", "http://localhost:54545/callback?state=b"],
  ["missing state", "http://localhost:54545/callback?code=a"],
] as const) {
  test(`rejects ${name}`, () => assert.throws(() => parseCallbackUrl(value), /callback URL/));
}
