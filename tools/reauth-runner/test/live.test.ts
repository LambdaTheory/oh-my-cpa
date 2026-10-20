import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { readVerificationCode } from "../src/live.js";

test("CODE_FILE waits until the file exists and contains a code", async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "omc-reauth-code-"));
  const codeFile = path.join(directory, "code");
  try {
    const result = readVerificationCode(codeFile, 2_000);
    await new Promise<void>((resolve) => setTimeout(resolve, 100));
    await fs.writeFile(codeFile, "\n", "utf8");
    await new Promise<void>((resolve) => setTimeout(resolve, 100));
    await fs.writeFile(codeFile, "  delayed-code\n", "utf8");
    assert.equal(await result, "delayed-code");
  } finally {
    await fs.rm(directory, { recursive: true, force: true });
  }
});

test("CODE_FILE reports a clear timeout while missing or empty", async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "omc-reauth-code-"));
  const codeFile = path.join(directory, "code");
  try {
    await fs.writeFile(codeFile, "\n", "utf8");
    await assert.rejects(readVerificationCode(codeFile, 150), /timed out after 150ms waiting for CODE_FILE/);
  } finally {
    await fs.rm(directory, { recursive: true, force: true });
  }
});
