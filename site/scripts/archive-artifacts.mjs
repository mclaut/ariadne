import { access, mkdir, rename, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { basename, join, resolve } from "node:path";

const args = new Set(process.argv.slice(2));
const dryRun = args.has("--dry-run");
const artifacts = [".next", "out", "dist", "build", ".wrangler"];
if (args.has("--dependencies")) artifacts.push("node_modules");

const projectRoot = process.cwd();
const archiveParent = resolve(
  process.env.ARIADNE_SITE_ARCHIVE_DIR || join(homedir(), ".ariadne", "archive", "site"),
);
const stamp = new Date().toISOString().replaceAll(":", "-").replaceAll(".", "-");
const archiveRoot = join(archiveParent, `${basename(resolve(projectRoot, ".."))}-${stamp}-${process.pid}`);
const present = [];

for (const name of artifacts) {
  const source = join(projectRoot, name);
  try {
    await access(source);
    present.push({ name, source, destination: join(archiveRoot, name) });
  } catch {
    // Missing generated artifacts are already clean.
  }
}

if (present.length === 0) {
  console.log("site artifacts: nothing to archive");
  process.exit(0);
}

if (dryRun) {
  for (const item of present) console.log(`${item.source} -> ${item.destination}`);
  process.exit(0);
}

await mkdir(archiveParent, { recursive: true, mode: 0o700 });
await mkdir(archiveRoot, { recursive: false, mode: 0o700 });
await writeFile(
  join(archiveRoot, "manifest.json"),
  `${JSON.stringify({ planned_at: new Date().toISOString(), project_root: projectRoot, artifacts: present }, null, 2)}\n`,
  { mode: 0o600, flag: "wx" },
);
for (const item of present) await rename(item.source, item.destination);
await writeFile(
  join(archiveRoot, "complete.json"),
  `${JSON.stringify({ completed_at: new Date().toISOString(), artifacts: present.map((item) => item.name) }, null, 2)}\n`,
  { mode: 0o600, flag: "wx" },
);

console.log(`site artifacts archived: ${archiveRoot}`);
