// The pure helpers of the harness choice (decision 0047): the rows of the
// harness step of the install board, from the harnesses of a plan or of a
// refused selection, the choice after a tick, and the harness answers of
// the wizard. They hold no rule of a selection: the server chooses the
// harnesses and refuses a choice it cannot install.

// isPreview says whether the harness x, as a plan or /api/harnesses gives
// it, is a preview: any status but supported counts as one, as the server
// counts it.
export const isPreview = (x) => x.status !== "supported";

// harnessRows are the rows of the harness step for list, the harnesses of
// a plan or of a harness-refused error, and chosen, the choice the user
// made, null for the plan's own. A required harness is ticked and locked,
// unless it is a preview: then only the user's tick opts it in, so it is
// ticked only when it is managed or chosen, as any preview harness is,
// and can be unticked. A harness that is not compatible or not installed
// cannot be ticked. why is the
// state the row shows: required, requiredMissing, requiredPreview,
// preview, notInstalled, notCompatible, or "".
export function harnessRows(list, chosen) {
  return (list || []).map((x) => {
    const preview = isPreview(x);
    const required = !!(x.compatible && x.required);
    const locked = required && !preview;
    const picked = x.compatible && x.detected && (chosen ? chosen.includes(x.id) : !!x.managed);
    let why = "";
    if (!x.compatible) why = "notCompatible";
    else if (required && !x.detected) why = "requiredMissing";
    else if (required) why = preview ? "requiredPreview" : "required";
    else if (!x.detected) why = "notInstalled";
    else if (preview) why = "preview";
    return {
      id: x.id, preview, required, locked, checked: !!(locked || picked), why,
      disabled: !x.compatible || !x.detected || locked,
      reason: preview ? x.statusReason || "" : "",
    };
  });
}

// pickOf is the choice rows show: the ids ticked, in the rows' order.
export const pickOf = (rows) => rows.filter((r) => r.checked).map((r) => r.id);

// toggled is the choice after the user ticks (on) or unticks the row of
// id: a locked harness stays in.
export function toggled(rows, id, on) {
  return pickOf(rows.map((r) => (r.id === id && !r.locked ? { ...r, checked: on } : r)));
}

// choiceOf is the choice to send for picks against list, the harnesses of
// the plan or refusal shown: null when picks are the ticks list shows on
// its own, so that the plan keeps the saved choice or the default and
// nothing is pinned; picks otherwise.
export function choiceOf(list, picks) {
  const own = pickOf(harnessRows(list, null));
  return own.length === picks.length && own.every((id, i) => id === picks[i]) ? null : picks;
}

// wizardDefaults are the harness answers the wizard starts with: every
// supported harness compatible, no preview one, and none required.
export function wizardDefaults(infos) {
  return { harnesses: infos.filter((x) => !isPreview(x)).map((x) => x.id), requires: [] };
}

// wizardRequest are the harness answers of the wizard's state: the
// compatible harnesses, and the required ones that are among them.
export function wizardRequest(state) {
  return { harnesses: [...state.harnesses], requires: state.harnesses.filter((id) => state.requires.includes(id)) };
}
