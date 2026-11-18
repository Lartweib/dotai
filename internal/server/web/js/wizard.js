// The pure helpers of the wizard board (decision 0046): the body of its
// plan and action from what the form holds, and the files of a plan by
// folder. They hold no rule of a pack: the server fills in the template,
// checks the names and validates the result.

// splitList reads a list the user typed, split by commas or lines,
// without blanks.
export function splitList(text) {
  return String(text ?? "").split(/[,\n]/).map((s) => s.trim()).filter(Boolean);
}

// emptyProfile is a profile of the form with nothing typed yet; hints and
// mods are the text of their fields.
export const emptyProfile = () => ({ name: "", description: "", hints: "", mods: "" });

// newRequest is the body of POST /api/packs/new/plan for the form's state:
// the directory, the name, the harnesses and the profiles, as dotai pack
// new takes them.
export function newRequest(state) {
  return {
    dir: state.dir.trim(),
    name: state.name.trim(),
    harnesses: [...state.harnesses],
    profiles: state.profiles.map((p) => ({
      name: p.name.trim(),
      description: p.description.trim(),
      hints: splitList(p.hints),
      mods: splitList(p.mods),
    })),
  };
}

// fileGroups groups the paths of a plan by their top folder, in the order
// the plan lists them, the files at the top of the pack first.
export function fileGroups(files) {
  const groups = new Map();
  for (const f of files) {
    const i = f.indexOf("/");
    const folder = i < 0 ? "" : f.slice(0, i);
    if (!groups.has(folder)) groups.set(folder, []);
    groups.get(folder).push(i < 0 ? f : f.slice(i + 1));
  }
  const top = groups.has("") ? [{ folder: "", files: groups.get("") }] : [];
  return [...top, ...[...groups].filter(([k]) => k !== "").map(([folder, list]) => ({ folder, files: list }))];
}
