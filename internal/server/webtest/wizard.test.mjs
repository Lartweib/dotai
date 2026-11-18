// Tests of js/wizard.js, the pure helpers of the wizard board (decision
// 0046). Run by TestTheWebModulesPassTheirTests (webtest_test.go) with
// node --test.

import assert from "node:assert/strict";
import { test } from "node:test";
import { emptyProfile, fileGroups, newRequest, splitList } from "../web/js/wizard.js";

test("splitList reads a list split by commas or lines, without blanks", () => {
  assert.deepEqual(splitList(""), []);
  assert.deepEqual(splitList(undefined), []);
  assert.deepEqual(splitList(" package.json, *.config.js ,\n go.mod\n,,"), ["package.json", "*.config.js", "go.mod"]);
});

test("newRequest is the body of the plan, as dotai pack new takes the answers", () => {
  const state = {
    dir: "  /home/u/packs/mine ",
    name: " mine ",
    harnesses: ["claudecode", "opencode"],
    profiles: [
      { name: " web ", description: " Web apps. ", hints: "package.json, *.config.js", mods: "panel" },
      { name: "go", description: "Go services.", hints: "", mods: "" },
    ],
  };
  assert.deepEqual(newRequest(state), {
    dir: "/home/u/packs/mine",
    name: "mine",
    harnesses: ["claudecode", "opencode"],
    profiles: [
      { name: "web", description: "Web apps.", hints: ["package.json", "*.config.js"], mods: ["panel"] },
      { name: "go", description: "Go services.", hints: [], mods: [] },
    ],
  });
  assert.notEqual(newRequest(state).harnesses, state.harnesses, "the body holds a copy of the harnesses");
});

test("emptyProfile is a profile with nothing typed yet", () => {
  assert.deepEqual(emptyProfile(), { name: "", description: "", hints: "", mods: "" });
  assert.notEqual(emptyProfile(), emptyProfile());
});

test("fileGroups groups the files by their top folder, the top's files first", () => {
  assert.deepEqual(fileGroups(["README.md", "base/a.md", "base/b/c.md", "dotai.yaml", "mods/panel/hooks/hooks.json", "profiles/web/x.md"]), [
    { folder: "", files: ["README.md", "dotai.yaml"] },
    { folder: "base", files: ["a.md", "b/c.md"] },
    { folder: "mods", files: ["panel/hooks/hooks.json"] },
    { folder: "profiles", files: ["web/x.md"] },
  ]);
  assert.deepEqual(fileGroups([]), []);
});
