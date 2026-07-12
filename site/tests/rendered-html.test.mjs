import assert from "node:assert/strict";
import { access, readFile } from "node:fs/promises";
import test from "node:test";

async function render(path = "/") {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}`);
  const { default: worker } = await import(workerUrl.href);

  return worker.fetch(
    new Request(`http://localhost${path}`, {
      headers: { accept: "text/html" },
    }),
    {
      ASSETS: {
        fetch: async () => new Response("Not found", { status: 404 }),
      },
    },
    {
      waitUntil() {},
      passThroughOnException() {},
    },
  );
}

test("server-renders the Ariadne product page and discovery metadata", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);

  const html = await response.text();
  assert.match(html, /<title>Ariadne - Local-first memory for AI agents<\/title>/i);
  assert.match(html, /<h1>Ariadne<\/h1>/);
  assert.match(html, /New in v0\.4\.0/);
  assert.match(html, /Content-free metrics/);
  assert.match(html, /application\/ld\+json/);
  assert.match(html, /SoftwareApplication/);
  assert.match(html, /og:image/);
  assert.doesNotMatch(html, /codex-preview|Your site is taking shape/);
});

test("keeps install, hosting, and social assets in the validated source", async () => {
  const [page, layout, hosting, og] = await Promise.all([
    readFile(new URL("../app/page.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/layout.tsx", import.meta.url), "utf8"),
    readFile(new URL("../.openai/hosting.json", import.meta.url), "utf8"),
    access(new URL("../public/og.png", import.meta.url)),
  ]);

  assert.match(page, /install\.ps1/);
  assert.match(page, /memory_recall/);
  assert.match(page, /Local memory map/);
  assert.doesNotMatch(page, /SkeletonPreview|codex-preview/);
  assert.match(layout, /summary_large_image/);
  assert.match(layout, /1280/);
  assert.equal(og, undefined);

  const config = JSON.parse(hosting);
  assert.equal(typeof config.project_id, "string");
  assert.ok(config.project_id.length > 0);

  const social = await render("/social-card");
  assert.equal(social.status, 200);
  assert.match(await social.text(), /Local-first memory for AI agents/i);
});
