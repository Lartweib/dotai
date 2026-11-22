// Tests of js/pathbreak.js, where an identity or a path may wrap. Run by
// TestTheWebModulesPassTheirTests (webtest_test.go) with node --test.

import assert from "node:assert/strict";
import { test } from "node:test";
import { isPathy, pathParts } from "../web/js/pathbreak.js";

test("an identity is cut after its slashes and nowhere else", () => {
  const parts = pathParts("github.com/lartweib/dotai-lartweib-pack");
  assert.deepEqual(parts.map((p) => p.text), ["github.com/", "lartweib/", "dotai-lartweib-pack"]);
  assert.equal(parts.map((p) => p.text).join(""), "github.com/lartweib/dotai-lartweib-pack");
  assert.ok(parts.every((p) => !p.free));
});

test("a Windows path is cut after its backslashes, and the parts join back to the text", () => {
  const text = "C:\\Users\\me\\.dotai\\packs\\x";
  const parts = pathParts(text);
  assert.deepEqual(parts.map((p) => p.text), ["C:\\", "Users\\", "me\\", ".dotai\\", "packs\\", "x"]);
  assert.equal(parts.map((p) => p.text).join(""), text);
});

test("a part with a space is free to wrap, a name is not", () => {
  const parts = pathParts("~/My Projects/app");
  assert.deepEqual(parts.map((p) => [p.text, p.free]), [["~/", false], ["My Projects/", true], ["app", false]]);
});

test("only a text with a separator inside it is a path", () => {
  assert.equal(isPathy("dotai-lartweib-pack"), false);
  assert.equal(isPathy("local/minimal-pack"), true);
  assert.equal(isPathy("base/"), false);
  assert.equal(isPathy(""), false);
  assert.equal(isPathy(undefined), false);
});
