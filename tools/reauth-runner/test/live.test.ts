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
    await fs.writeFile(codeFile, "  123456\n", "utf8");
    assert.equal(await result, "123456");
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

test("CODE_FILE refuses non-six-digit values without printing their contents", async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "omc-reauth-code-"));
  try {
    const codeFile = path.join(directory, "code");
    await fs.writeFile(codeFile, "sensitive-invalid-value");
    await assert.rejects(readVerificationCode(codeFile, 1000), (error: unknown) =>
      error instanceof Error && /exactly six digits/.test(error.message) && !error.message.includes("sensitive-invalid-value"));
  } finally { await fs.rm(directory, { recursive: true, force: true }); }
});
