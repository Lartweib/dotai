// What several boards share: one vocabulary for what a plan does to a
// path, how a path reads, the guard of an action button, and the preview
// then confirm of a restore. The boards import these from here, never
// from each other.

import { busy, h, mount, notice, op, plural, warning } from "./dom.js";
import { act, api, query, tilde } from "./store.js";

// actionWords are the words the boards show for the actions of the plans.
export const actionWords = {
  create: "create",
  replace: "overwrite",
  remove: "remove",
  unchanged: "keep",
  merge: "merge",
  conflict: "conflict",
  wipe: "wipe",
};

export const word = (action) => actionWords[action] || action;

// rel is target relative to dir, with forward slashes, or target as it
// reads from the home directory when it is not inside dir.
export function rel(dir, target) {
  const d = (dir || "").replace(/\\/g, "/").replace(/\/$/, "");
  const t = (target || "").replace(/\\/g, "/");
  return d && t.toLowerCase().startsWith(d.toLowerCase() + "/") ? t.slice(d.length + 1) : tilde(target);
}

// guard is an action button that its own request disables: allowed says
// whether the plan lets the user act, and sync never enables the button
// while the request runs.
export function guard(button, allowed) {
  let running = false;
  const sync = () => { button.disabled = running || !allowed(); };
  return {
    sync,
    async run(request) {
      if (running) return null;
      running = true;
      sync();
      try {
        return await request();
      } finally {
        running = false;
        sync();
      }
    },
  };
}

// confirmFlow shows a preview in el, with its conflicts and the consent
// they need, and carries out exactly that preview: f.load gives it,
// f.rows lists what it writes, f.execute(preview, yes) carries it out. A
// preview that changed before the restore wrote anything is shown again,
// new.
export async function confirmFlow(el, f, msg) {
  mount(el, busy("Previewing, nothing is written..."));
  const res = await f.load();
  if (f.ctx.stale()) return;
  if (!res.ok) {
    mount(el, notice("bad", res.error.code, res.error.message), conflictList(res.data && res.data.conflicts));
    return;
  }
  showPreview(el, f, res.data, msg);
}

function showPreview(el, f, r, msg) {
  const conflicts = r.conflicts || [];
  const yes = h("input", { type: "checkbox" });
  const out = h("div");
  const go = h("button", { class: "btn primary", type: "button", text: f.label });
  const g = guard(go, () => conflicts.length === 0 || yes.checked);
  yes.addEventListener("change", g.sync);
  g.sync();
  go.addEventListener("click", async () => {
    mount(out, busy("Restoring..."));
    const res = await g.run(() => f.execute(r, yes.checked));
    if (!res || f.ctx.stale()) return;
    if (res.ok) {
      await f.ctx.refresh();
      if (f.ctx.stale()) return;
      mount(el, notice("good", "restored", res.data.backup ? "Done. The backup " + res.data.backup.id + " holds what the restore overwrote or removed." : "Done. The restore wrote no file."),
        (res.data.warnings || []).length ? h("ul", {}, res.data.warnings.map((w) => warning("warning", w))) : null,
        h("div", { class: "row" }, h("button", { class: "btn", type: "button", onclick: () => f.ctx.again(), text: "Show the board again" })));
      return;
    }
    if (res.error.code === "plan-changed") {
      showPreview(el, f, res.data, notice("bad", res.error.code, res.error.message));
      return;
    }
    mount(out, notice("bad", res.error.code, res.error.message), conflictList(res.data && res.data.conflicts));
  });
  const rows = f.rows(r).filter(Boolean);
  mount(el, msg || null,
    h("p", { class: "body", text: f.lead(r) }),
    h("ul", {}, rows.length ? rows : h("li", { class: "quietp", text: "Nothing to write." })),
    (r.warnings || []).length ? h("ul", {}, r.warnings.map((w) => warning("warning", w))) : null,
    conflicts.length ? h("div", { class: "well" }, h("p", { class: "t-label", text: plural(conflicts.length, "conflict") }), conflictList(conflicts)) : null,
    conflicts.length ? h("label", { class: "check" }, yes, h("span", { text: "Overwrite the conflicts. The restore copies each one into its own backup first." })) : null,
    h("div", { class: "row" }, go), h("p", { class: "t-cap", text: f.says }), out);
}

function conflictList(conflicts) {
  if (!conflicts || conflicts.length === 0) return null;
  return h("ul", {}, conflicts.map((c) => warning(c.key ? "key-changed" : "file-changed",
    tilde(c.target) + (c.key ? " · " + c.key : "") + ": changed since dotai wrote it.")));
}

// restoreHomeFlow is the preview then confirm of dotai restore of the
// backup id; the server refuses what cannot be restored.
export function restoreHomeFlow(id, ctx) {
  return {
    load: () => api(query("/api/restore/preview", { id }), { signal: ctx.signal }),
    lead: (r) => "Restore undoes " + plural(r.undone.length, "install") + (r.uninstalled ? ", and no install is left after it." : ", and puts back the install before them."),
    rows: (r) => [
      ...r.ops.map((o) => op(word(o.action), tilde(o.target))),
      ...r.wipes.map((w) => op(word("create"), tilde(w.path), "moved back from the backup")),
      ...r.wipeKeys.map((k) => op(word("replace"), tilde(k.file) + " · " + k.key.join("."), "key put back")),
    ],
    label: "Restore",
    says: "Restore first takes a backup of its own, <time>_restore, of every file it removes or overwrites.",
    execute: (r, yes) => act("/api/restore", { id, digest: r.digest, yes }),
    ctx,
  };
}

// restoreProjectFlow is the preview then confirm of dotai restore
// --project in project.
export function restoreProjectFlow(project, ctx) {
  return {
    load: () => api(query("/api/restore/project/preview", { project }), { signal: ctx.signal }),
    lead: () => "A project is restored as a whole: every apply recorded there is undone, so the project, its exclude file and the keys dotai owns are as before the first apply.",
    rows: (r) => [
      ...r.ops.map((o) => op(o.conflict ? word("conflict") : word(o.action), rel(project, o.target))),
      r.exclude.file ? op(word("replace"), tilde(r.exclude.file), "the project's block leaves it") : null,
    ],
    label: "Restore project",
    says: "Runs dotai restore --project for " + tilde(project) + ", after a backup of what it writes.",
    execute: (r, yes) => act("/api/restore/project", { project, digest: r.digest, yes }),
    ctx,
  };
}
