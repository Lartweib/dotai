// Tests of js/packsource.js, the pure helpers of the pack boards. Run by
// TestTheWebModulesPassTheirTests (webtest_test.go) with node --test.

import assert from "node:assert/strict";
import { test } from "node:test";
import { addCommand, diffLines, failureKind, gitIdentity, modeChanged, moves, planNotes, refProblem, shortCommit, shortDigest, tally, visible } from "../web/js/packsource.js";

test("gitIdentity reads the three URL forms of github.com, in lower case", () => {
  for (const url of [
    "https://github.com/Acme/My-Pack.git",
    "https://github.com/acme/my-pack",
    "https://github.com/acme/my-pack/",
    "ssh://git@github.com/acme/my-pack.git",
    "git@github.com:ACME/my-pack",
    "  https://github.com/acme/my-pack  ",
  ]) {
    assert.equal(gitIdentity(url), "github.com/acme/my-pack", url);
  }
});

test("gitIdentity gives nothing for what is not a github.com repository", () => {
  for (const url of [
    "",
    "https://gitlab.com/acme/pack",
    "https://github.com/acme",
    "https://github.com/acme/pack/tree/main",
    "http://github.com/acme/pack",
    "https://github.com:443/acme/pack",
    "https://user@github.com/acme/pack",
    "C:\\packs\\pack",
    "/home/u/pack",
    "https://github.com/acme/pack.",
  ]) {
    assert.equal(gitIdentity(url), "", url);
  }
});

test("refProblem asks for a name, or for a full commit SHA", () => {
  assert.equal(refProblem("default", ""), "");
  assert.equal(refProblem("named", "v1.2.0"), "");
  assert.equal(refProblem("named", "refs/tags/v1.2.0"), "");
  assert.equal(refProblem("named", ""), "ref-empty");
  assert.equal(refProblem("named", "  "), "ref-empty");
  assert.equal(refProblem("named", "-x"), "ref-dash");
  assert.equal(refProblem("named", "a b"), "ref-space");
  assert.equal(refProblem("commit", "a".repeat(40)), "");
  assert.equal(refProblem("commit", "A".repeat(64)), "");
  assert.equal(refProblem("commit", "a".repeat(12)), "commit-full");
  assert.equal(refProblem("commit", "z".repeat(40)), "commit-full");
  assert.equal(refProblem("commit", ""), "commit-full");
});

test("shortCommit keeps the first twelve digits, as the command line prints them", () => {
  assert.equal(shortCommit("0123456789abcdef0123456789abcdef01234567"), "0123456789ab");
  assert.equal(shortCommit("abc"), "abc");
  assert.equal(shortCommit(""), "");
  assert.equal(shortCommit(undefined), "");
});

test("diffLines drops the file headers and marks each line", () => {
  const diff = "--- a/x.sh\n+++ b/x.sh\n@@ -1,2 +1,2 @@\n keep\n-old\n+new\n\\ No newline at end of file\n";
  assert.deepEqual(diffLines(diff), [
    { kind: "hunk", sign: "", text: "@@ -1,2 +1,2 @@" },
    { kind: "ctx", sign: " ", text: "keep" },
    { kind: "del", sign: "-", text: "old" },
    { kind: "add", sign: "+", text: "new" },
    { kind: "note", sign: "", text: "\\ No newline at end of file" },
  ]);
});

test("diffLines keeps a removed line that reads like a header once a hunk began", () => {
  const diff = "--- a/x\n+++ /dev/null\n@@ -1 +0,0 @@\n--- not a header\n";
  assert.deepEqual(diffLines(diff), [
    { kind: "hunk", sign: "", text: "@@ -1 +0,0 @@" },
    { kind: "del", sign: "-", text: "-- not a header" },
  ]);
});

test("diffLines of no diff is no line", () => {
  assert.deepEqual(diffLines(""), []);
  assert.deepEqual(diffLines(undefined), []);
});

test("visible escapes what could hide or reorder text, and keeps the rest", () => {
  assert.equal(visible("base/dotai/tools/x.sh"), "base/dotai/tools/x.sh");
  assert.equal(visible("a\x1b[31mb"), "a\\x1b[31mb");
  assert.equal(visible("a\nb\tc"), "a\\x0ab\\x09c");
  assert.equal(visible("x\u202ey"), "x\\u202ey");
  assert.equal(visible("x\u2066y"), "x\\u2066y");
  assert.equal(visible("x\x7fy\x85z"), "x\\x7fy\\x85z");
  assert.equal(visible("\u00f1and\u00fa/\u00e9"), "\u00f1and\u00fa/\u00e9");
});

test("modeChanged needs both modes, and different ones", () => {
  assert.equal(modeChanged({ modeBefore: "100644", modeAfter: "100755" }), true);
  assert.equal(modeChanged({ modeBefore: "100755", modeAfter: "100755" }), false);
  assert.equal(modeChanged({ modeBefore: "", modeAfter: "100755" }), false);
});

test("tally counts the changes of a plan by kind, and the modes that changed", () => {
  const changes = [
    { change: "added", modeBefore: "", modeAfter: "100755" },
    { change: "modified", modeBefore: "100644", modeAfter: "100755" },
    { change: "modified", modeBefore: "100644", modeAfter: "100644" },
    { change: "removed", modeBefore: "100644", modeAfter: "" },
  ];
  assert.deepEqual(tally(changes), { added: 1, modified: 2, removed: 1, modes: 1, binary: 0 });
  assert.deepEqual(tally([{ change: "added", binary: true }]), { added: 1, modified: 0, removed: 0, modes: 0, binary: 1 });
  assert.deepEqual(tally(undefined), { added: 0, modified: 0, removed: 0, modes: 0, binary: 0 });
});

test("addCommand is the command line that adds the same pack, quoted for a POSIX shell", () => {
  assert.equal(addCommand("https://github.com/acme/pack", ""), "dotai pack add 'https://github.com/acme/pack'");
  assert.equal(addCommand(" https://github.com/acme/pack ", " v1 "), "dotai pack add 'https://github.com/acme/pack' --ref 'v1'");
  assert.equal(addCommand("git@github.com:acme/pack", "it's"), "dotai pack add 'git@github.com:acme/pack' --ref 'it'\\''s'");
});

test("addCommand gives nothing for an address that is not a github.com repository", () => {
  assert.equal(addCommand("https://example.com/x; rm -rf ~", ""), "");
  assert.equal(addCommand("", "v1"), "");
});

test("visible escapes the invisible format characters too", () => {
  for (const c of ["\u200b", "\u200c", "\u200d", "\u200e", "\u200f", "\u2028", "\u2029", "\u2060", "\u061c", "\ufeff"]) {
    const want = "a\\u" + c.charCodeAt(0).toString(16).padStart(4, "0") + "b";
    assert.equal(visible("a" + c + "b"), want, want);
  }
});

test("moves tells a plan that takes the pack to another commit, or fetches a gone copy", () => {
  assert.equal(moves({ from: { commit: "a" }, to: { commit: "b" }, missing: false }), true);
  assert.equal(moves({ from: { commit: "a" }, to: { commit: "a" }, missing: false }), false);
  assert.equal(moves({ from: { commit: "a" }, to: { commit: "a" }, missing: true }), true);
});

test("shortDigest keeps twelve hex digits of a digest", () => {
  assert.equal(shortDigest("sha256:0123456789abcdef"), "0123456789ab");
  assert.equal(shortDigest(""), "");
  assert.equal(shortDigest(undefined), "");
});

test("planNotes merges the warnings of a plan with the notices outside it", () => {
  const w = { code: "ref-branch", message: "m" };
  const n = { code: "pack-cleanup-failed", message: "n" };
  assert.deepEqual(planNotes({ warnings: [w], notices: [n] }), [w, n]);
  assert.deepEqual(planNotes({ warnings: [w] }), [w]);
  assert.deepEqual(planNotes({}), []);
});

test("failureKind sorts the failures of a pack fetch", () => {
  assert.equal(failureKind({ error: { code: "internal" }, data: { timedOut: true } }), "timeout");
  assert.equal(failureKind({ error: { code: "internal", message: "x" } }), "plain");
  assert.equal(failureKind({ error: { code: "network", maybeRan: true } }), "lost");
  assert.equal(failureKind({ error: { code: "bad-response" } }), "lost");
  assert.equal(failureKind({ error: { code: "busy" } }), "busy");
  assert.equal(failureKind({ error: { code: "invalid" }, data: { diagnostics: [] } }), "invalid");
  assert.equal(failureKind({ error: { code: "invalid" } }), "plain");
  assert.equal(failureKind({ error: { code: "pack-tree-refused" }, data: { problems: [] } }), "tree");
  assert.equal(failureKind({ error: { code: "fetch-failed" } }), "plain");
  assert.equal(failureKind({}), "plain");
});
