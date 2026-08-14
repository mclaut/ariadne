import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";

const script = join(dirname(fileURLToPath(import.meta.url)), "..", "scripts", "archive-artifacts.mjs");

test("site cleanup archives artifacts and writes a recovery manifest", async () => {
  const root = await mkdtemp(join(tmpdir(), "ariadne-site-clean-"));
  const site = join(root, "site");
  const archive = join(root, "archive");
  await mkdir(join(site, "dist"), { recursive: true });
  await mkdir(join(site, "node_modules"), { recursive: true });
  await writeFile(join(site, "dist", "index.html"), "generated");

  const result = spawnSync(process.execPath, [script, "--dependencies"], {
    cwd: site,
    env: { ...process.env, ARIADNE_SITE_ARCHIVE_DIR: archive },
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  const archivedRoot = result.stdout.trim().replace("site artifacts archived: ", "");
  assert.equal((await stat(join(archivedRoot, "dist", "index.html"))).isFile(), true);
  assert.equal((await stat(join(archivedRoot, "node_modules"))).isDirectory(), true);
  const manifest = JSON.parse(await readFile(join(archivedRoot, "manifest.json"), "utf8"));
  assert.deepEqual(manifest.artifacts.map((item) => item.name), ["dist", "node_modules"]);
  const complete = JSON.parse(await readFile(join(archivedRoot, "complete.json"), "utf8"));
  assert.deepEqual(complete.artifacts, ["dist", "node_modules"]);
});
