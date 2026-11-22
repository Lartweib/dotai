// Tests of the words the add and update boards show for the failures of
// --ref latest (decision 0054): js/i18n.js errorText. Run by
// TestTheWebModulesPassTheirTests (webtest_test.go) with node --test.

import assert from "node:assert/strict";
import { test } from "node:test";

// i18n.js sets the lang of the page when it loads.
globalThis.document = { documentElement: {} };
const { errorText, setLang } = await import("../web/js/i18n.js");
const { failureKind } = await import("../web/js/packsource.js");

const noTag = { code: "no-release-tag", message: "https://github.com/a/b has no release tag" };
const down = { code: "latest-downgrade", message: "the latest release of github.com/a/b is v1.0.0, below v1.1.0" };

test("a repository with no release tag is explained, with the server's message as detail", () => {
  setLang("en");
  const e = errorText(noTag);
  assert.match(e.text, /no release tag of the form vX\.Y\.Z/);
  assert.match(e.text, /default branch/);
  assert.equal(e.detail, "no-release-tag: " + noTag.message);
  setLang("es");
  assert.match(errorText(noTag).text, /etiqueta de versión con la forma vX\.Y\.Z/);
  setLang("en");
});

test("a latest pack that would go down is refused in words that say how to go down", () => {
  setLang("en");
  assert.match(errorText(down).text, /does not go down by itself/);
  assert.equal(errorText(down).detail, "latest-downgrade: " + down.message);
  setLang("es");
  assert.match(errorText(down).text, /no baja por sí solo/);
  setLang("en");
});

test("both failures are plain failures of the board, with no data to list", () => {
  assert.equal(failureKind({ ok: false, error: noTag }), "plain");
  assert.equal(failureKind({ ok: false, error: down }), "plain");
});
