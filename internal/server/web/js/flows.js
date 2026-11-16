// What several boards share: one vocabulary for what a plan does to a
// path, how a path reads, the guard of an action button, and the preview
// then confirm of a restore. The boards import these from here, never
// from each other.

import { actions, busy, failure, h, loading, mount, notice, op, warning } from "./dom.js";
import { t } from "./i18n.js";
import { act, api, query, tilde } from "./store.js";

// word is the word the boards show for an action of a plan.
export const word = (action) => t("action." + action);

// rel is target relative to dir, with forward slashes, or target as it
// reads from the home directory when it is not inside dir.
export function rel(dir, target) {
  const d = (dir || "").replace(/\\/g, "/").replace(/\/$/, "");
  const p = (target || "").replace(/\\/g, "/");
  return d && p.toLowerCase().startsWith(d.toLowerCase() + "/") ? p.slice(d.length + 1) : tilde(target);
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
  loading(el, t("restore.previewing"));
  const res = await f.load();
  if (f.ctx.stale()) return;
  if (!res.ok) {
    mount(el, failure(res.error), conflictList(res.data && res.data.conflicts));
    return;
  }
  showPreview(el, f, res.data, msg);
}

function showPreview(el, f, r, msg) {
  const conflicts = r.conflicts || [];
  const yes = h("input", { type: "checkbox" });
  const out = h("div", { class: "out" });
  const go = h("button", { class: "btn primary", type: "button", text: f.label });
  const g = guard(go, () => conflicts.length === 0 || yes.checked);
  yes.addEventListener("change", g.sync);
  g.sync();
  go.addEventListener("click", async () => {
    mount(out, busy(t("restore.running")));
    const res = await g.run(() => f.execute(r, yes.checked));
    if (!res || f.ctx.stale()) return;
    if (res.ok) {
      await f.ctx.refresh();
      if (f.ctx.stale()) return;
      mount(el, notice("good", res.data.backup ? t("restore.done", { id: res.data.backup.id }) : t("restore.doneNothing")),
        (res.data.warnings || []).length ? h("ul", { class: "list" }, warning("notice", t("plan.notices", { n: res.data.warnings.length }), res.data.warnings.join("\n"))) : null,
        actions([h("button", { class: "btn", type: "button", onclick: () => f.ctx.again(), text: t("common.showAgain") })]));
      return;
    }
    if (res.error.code === "plan-changed") {
      showPreview(el, f, res.data, failure(res.error));
      return;
    }
    mount(out, failure(res.error), conflictList(res.data && res.data.conflicts));
  });
  const rows = f.rows(r).filter(Boolean);
  mount(el, h("div", { class: "stack" }, msg || null,
    h("p", { class: "body", text: f.lead(r) }),
    h("ul", { class: "list" }, rows.length ? rows : h("li", { class: "quietp", text: t("common.nothingToWrite") })),
    (r.warnings || []).length ? h("ul", { class: "list" }, warning("notice", t("plan.notices", { n: r.warnings.length }), r.warnings.join("\n"))) : null,
    conflicts.length ? h("div", { class: "well" }, h("p", { class: "t-label", text: t("restore.conflicts", { n: conflicts.length }) }), conflictList(conflicts)) : null,
    conflicts.length ? h("label", { class: "check" }, yes, h("span", { text: t("restore.overwrite") })) : null,
    actions([go], f.says), out));
}

function conflictList(conflicts) {
  if (!conflicts || conflicts.length === 0) return null;
  return h("ul", { class: "list" }, conflicts.map((c) => warning(c.key ? "key-changed" : "file-changed",
    t("restore.conflict", { path: tilde(c.target) + (c.key ? " · " + c.key : "") }))));
}

// restoreHomeFlow is the preview then confirm of dotai restore of the
// backup id; the server refuses what cannot be restored.
export function restoreHomeFlow(id, ctx) {
  return {
    load: () => api(query("/api/restore/preview", { id }), { signal: ctx.signal }),
    lead: (r) => t(r.uninstalled ? "restore.homeLeadNone" : "restore.homeLead", { n: r.undone.length }),
    rows: (r) => [
      ...r.ops.map((o) => op(o.action, tilde(o.target))),
      ...r.wipes.map((w) => op("create", tilde(w.path), t("restore.movedBack"))),
      ...r.wipeKeys.map((k) => op("replace", tilde(k.file) + " · " + k.key.join("."), t("restore.keyBack"))),
    ],
    label: t("restore.go"),
    says: t("restore.homeSays"),
    execute: (r, yes) => act("/api/restore", { id, digest: r.digest, yes }),
    ctx,
  };
}

// restoreProjectFlow is the preview then confirm of dotai restore
// --project in project.
export function restoreProjectFlow(project, ctx) {
  return {
    load: () => api(query("/api/restore/project/preview", { project }), { signal: ctx.signal }),
    lead: () => t("restore.projectLead"),
    rows: (r) => [
      ...r.ops.map((o) => op(o.conflict ? "conflict" : o.action, rel(project, o.target))),
      r.exclude.file ? op("replace", tilde(r.exclude.file), t("restore.excludeLeaves")) : null,
    ],
    label: t("restore.goProject"),
    says: t("restore.projectSays", { path: tilde(project) }),
    execute: (r, yes) => act("/api/restore/project", { project, digest: r.digest, yes }),
    ctx,
  };
}
