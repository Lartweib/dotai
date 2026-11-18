// What an install plan asks consent for, from the plan alone: what a
// replace install moves into its backup that dotai does not own
// (consentForeign, decision 0038), and the files of the pack's template it
// installs unchanged (its template-unchanged warnings, decision 0046).

// templateFiles are the files of the template the plan p installs
// unchanged, by their path in the pack.
export function templateFiles(p) {
  return (p.warnings || []).filter((w) => w.code === "template-unchanged").map((w) => w.item);
}

// foreignWarned says whether the plan p warns that it moves into its
// backup what dotai does not own: only when that is part of its consent.
export function foreignWarned(p) {
  return !!(p.consent && p.consentForeign);
}

// consentLabel is the i18n key of the consent checkbox of the plan p,
// which names what the user accepts, and n, the number of template files.
export function consentLabel(p) {
  const n = templateFiles(p).length;
  if (foreignWarned(p)) return { key: n ? "plan.consentBoth" : "plan.consentForeign", n };
  return { key: "plan.consentTemplate", n };
}
