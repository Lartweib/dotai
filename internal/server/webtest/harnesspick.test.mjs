// Tests of js/harnesspick.js, the harness step of the install board and
// the harness answers of the wizard (decision 0047). Run by
// TestTheWebModulesPassTheirTests (webtest_test.go) with node --test.

import assert from "node:assert/strict";
import { test } from "node:test";
import { appliedRows, choiceOf, harnessRows, isPreview, pickOf, toggled, wizardDefaults, wizardRequest } from "../web/js/harnesspick.js";

const states = [
  { id: "claudecode", compatible: true, required: true, status: "supported", statusReason: "", managed: true, detected: true },
  { id: "antigravity-cli", compatible: true, required: false, status: "supported", statusReason: "", managed: true, detected: true },
  { id: "opencode", compatible: true, required: false, status: "preview", statusReason: "hooks hang", managed: false, detected: true },
];

test("harnessRows shows each harness with its state: required ones locked on, previews unticked with their reason", () => {
  const rows = harnessRows([...states, { id: "x", compatible: false, required: false, status: "supported", statusReason: "", managed: false, detected: true }], null);
  assert.deepEqual(rows.map((r) => [r.id, r.checked, r.disabled, r.why]), [
    ["claudecode", true, true, "required"],
    ["antigravity-cli", true, false, ""],
    ["opencode", false, false, "preview"],
    ["x", false, true, "notCompatible"],
  ]);
  assert.equal(rows[2].reason, "hooks hang");
});

test("harnessRows marks a compatible harness that is not installed, and a required one that is missing", () => {
  const rows = harnessRows([
    { id: "claudecode", compatible: true, required: true, status: "supported", statusReason: "", managed: false, detected: false },
    { id: "antigravity-cli", compatible: true, required: false, status: "supported", statusReason: "", managed: false, detected: false },
  ], null);
  assert.deepEqual(rows.map((r) => [r.id, r.checked, r.disabled, r.why]), [
    ["claudecode", true, true, "requiredMissing"],
    ["antigravity-cli", false, true, "notInstalled"],
  ]);
});

test("a choice ticks what it names, and keeps the required ones on", () => {
  const rows = harnessRows(states, ["opencode"]);
  assert.deepEqual(rows.map((r) => [r.id, r.checked]), [["claudecode", true], ["antigravity-cli", false], ["opencode", true]]);
});

test("toggled is the choice after a tick, in the rows' order, required ones always in", () => {
  const rows = harnessRows(states, null);
  assert.deepEqual(toggled(rows, "opencode", true), ["claudecode", "antigravity-cli", "opencode"]);
  assert.deepEqual(toggled(rows, "antigravity-cli", false), ["claudecode"]);
  assert.deepEqual(toggled(rows, "claudecode", false), ["claudecode", "antigravity-cli"], "a required harness cannot be left out");
});

test("a required preview harness no one chose is unticked and can be ticked, with its reason", () => {
  const list = [
    { id: "claudecode", compatible: true, required: false, status: "supported", statusReason: "", managed: true, detected: true },
    { id: "opencode", compatible: true, required: true, status: "preview", statusReason: "hooks hang", managed: false, detected: true },
  ];
  const rows = harnessRows(list, null);
  assert.deepEqual(rows.map((r) => [r.id, r.checked, r.disabled, r.why, r.reason]), [
    ["claudecode", true, false, "", ""],
    ["opencode", false, false, "requiredPreview", "hooks hang"],
  ]);
  assert.deepEqual(toggled(rows, "opencode", true), ["claudecode", "opencode"]);
  assert.deepEqual(harnessRows(list, ["claudecode", "opencode"]).map((r) => [r.id, r.checked, r.disabled]), [["claudecode", true, false], ["opencode", true, false]]);
  assert.deepEqual(toggled(harnessRows(list, ["claudecode", "opencode"]), "opencode", false), ["claudecode"], "unticking it again is allowed, and keeps the refusal");
});

test("a status other than supported counts as a preview", () => {
  assert.equal(isPreview({ status: "supported" }), false);
  assert.equal(isPreview({ status: "preview" }), true);
  assert.equal(isPreview({ status: "" }), true);
  const rows = harnessRows([{ id: "x", compatible: true, required: false, status: "", statusReason: "", managed: false, detected: true }], null);
  assert.deepEqual(rows.map((r) => [r.checked, r.why]), [[false, "preview"]]);
});

test("choiceOf sends no choice when the ticks are the plan's own, and the ticks otherwise", () => {
  assert.equal(choiceOf(states, pickOf(harnessRows(states, null))), null, "planning again with no tick changed pins nothing");
  const rows = harnessRows(states, null);
  assert.deepEqual(choiceOf(states, toggled(rows, "opencode", true)), ["claudecode", "antigravity-cli", "opencode"]);
  const back = toggled(harnessRows(states, ["claudecode", "antigravity-cli", "opencode"]), "opencode", false);
  assert.equal(choiceOf(states, back), null, "ticking back to the plan's own sends none");
});

test("wizardDefaults ticks every supported harness, no preview one, and requires none", () => {
  const infos = [{ id: "claudecode", status: "supported" }, { id: "antigravity-cli", status: "supported" }, { id: "opencode", status: "preview" }];
  assert.deepEqual(wizardDefaults(infos), { harnesses: ["claudecode", "antigravity-cli"], requires: [] });
});

test("wizardRequest keeps the required harnesses that are compatible, in the harnesses' order", () => {
  assert.deepEqual(wizardRequest({ harnesses: ["opencode", "claudecode"], requires: ["claudecode", "antigravity-cli"] }),
    { harnesses: ["opencode", "claudecode"], requires: ["claudecode"] });
});

test("appliedRows shows each harness of an apply plan with its state, planned or not, and no box to tick", () => {
  const rows = appliedRows([
    { id: "claudecode", compatible: true, required: true, status: "supported", statusReason: "", managed: true, detected: true },
    { id: "opencode", compatible: true, required: false, status: "preview", statusReason: "hooks hang", managed: false, detected: true },
    { id: "antigravity-cli", compatible: true, required: false, status: "supported", statusReason: "", managed: false, detected: false },
    { id: "x", compatible: false, required: false, status: "supported", statusReason: "", managed: false, detected: true },
    { id: "y", compatible: true, required: false, status: "supported", statusReason: "", managed: false, detected: true },
  ]);
  assert.deepEqual(rows.map((r) => [r.id, r.states, r.reason]), [
    ["claudecode", ["managed", "required"], ""],
    ["opencode", ["notManaged", "preview"], "hooks hang"],
    ["antigravity-cli", ["notManaged", "notInstalled"], ""],
    ["x", ["notCompatible"], ""],
    ["y", ["notManaged"], ""],
  ]);
  assert.deepEqual(appliedRows(undefined), [], "a plan from before the states lists none");
});
