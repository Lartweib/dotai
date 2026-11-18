// Tests of js/planconsent.js, what an install plan asks consent for
// (decisions 0038 and 0046). Run by TestTheWebModulesPassTheirTests
// (webtest_test.go) with node --test.

import assert from "node:assert/strict";
import { test } from "node:test";
import { consentLabel, foreignWarned, templateFiles } from "../web/js/planconsent.js";

const templateWarning = { code: "template-unchanged", item: "base/.rulesync/rules/example-rule.md", message: "..." };

test("a replace plan whose only consent is template files warns of no foreign content", () => {
  const p = { mode: "replace", consent: true, consentForeign: false, wipes: [], wipeKeys: [], warnings: [templateWarning] };
  assert.equal(foreignWarned(p), false);
  assert.deepEqual(templateFiles(p), ["base/.rulesync/rules/example-rule.md"]);
  assert.deepEqual(consentLabel(p), { key: "plan.consentTemplate", n: 1 });
});

test("a replace plan with foreign content warns of it, and its label names what it accepts", () => {
  const p = { mode: "replace", consent: true, consentForeign: true, wipes: ["~/.claude/x"], wipeKeys: [], warnings: [] };
  assert.equal(foreignWarned(p), true);
  assert.deepEqual(consentLabel(p), { key: "plan.consentForeign", n: 0 });
  const both = { ...p, warnings: [templateWarning, { ...templateWarning, item: "variants/default/.rulesync/rules/x.md" }, { code: "notice", item: "" }] };
  assert.deepEqual(consentLabel(both), { key: "plan.consentBoth", n: 2 });
});

test("a plan that needs no consent warns of nothing", () => {
  const p = { mode: "replace", consent: false, consentForeign: false, warnings: [] };
  assert.equal(foreignWarned(p), false);
});
