import assert from "node:assert/strict";
import test from "node:test";
import { runFixtureFlow } from "../src/fixture.js";

test("fixture fills both forms and captures the unreachable loopback callback", { timeout: 30_000 }, async () => {
  assert.deepEqual(await runFixtureFlow(20_000), {
    code: "fixture-code",
    state: "fixture-state",
  });
});
