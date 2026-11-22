// Tests that the pack the UI shows is not stale after an action rewrote
// it in place (js/store.js). Run by TestTheWebModulesPassTheirTests
// (webtest_test.go) with node --test.

import assert from "node:assert/strict";
import { test } from "node:test";

globalThis.document = { documentElement: {} };
globalThis.sessionStorage = { getItem: () => "tok" };
globalThis.localStorage = { getItem: () => null, setItem() {} };

const path = "/packs/demo";
let version = "0.1.0";
globalThis.fetch = async (url) => {
  const u = String(url);
  const data = u.startsWith("/api/pack/summary") ? { outline: version } : u.startsWith("/api/pack?") ? { pack: { version } } : { pack: { path } };
  return { status: 200, json: async () => ({ ok: true, data }) };
};

const { act, loadOutline, loadPack } = await import("../web/js/store.js");

test("a pack update of the same path shows the new version on the next read", async () => {
  assert.equal((await loadPack(path)).data.pack.version, "0.1.0");
  assert.equal((await loadOutline(path)).data.outline, "0.1.0");
  version = "0.1.1";
  assert.equal((await loadPack(path)).data.pack.version, "0.1.0", "cached until an action rewrites it");

  const res = await act("/api/packs/add", { url: "github.com/a/b", ref: "latest" });
  assert.ok(res.ok);

  assert.equal((await loadPack(path)).data.pack.version, "0.1.1");
  assert.equal((await loadOutline(path)).data.outline, "0.1.1");
});
