// The Backups board: every backup, newest first, the home ones and those
// of each project. A home backup restores through a preview first: the
// restore carries out only the preview it shows, by its digest, and the
// server refuses what cannot be restored. A project is restored as a
// whole, through the preview of restore --project.

import { actions, boardHead, busy, failure, h, hint, kv, mount, notice, sec, settle } from "./dom.js";
import { confirmFlow, restoreHomeFlow, restoreProjectFlow } from "./flows.js";
import { fmtDate, fmtRelative, lower, t } from "./i18n.js";
import { api, harnessName, tilde } from "./store.js";

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-label": t("backups.listLabel") });
  const detail = h("section", { class: "detail", "aria-label": t("backups.detailLabel") });
  mount(root, boardHead("backups"), list, detail);
  mount(list, busy(t("backups.reading")));
  const res = await api("/api/backups", { signal: ctx.signal });
  if (ctx.stale()) return;
  if (!res.ok) {
    mount(list, failure(res.error));
    return;
  }
  const groups = [{ scope: "home", label: t("backups.home"), dir: "", project: "", items: res.data.home },
    ...res.data.projects.map((p) => ({ scope: "project", label: p.project ? tilde(p.project) : t("backups.unknownProject"), dir: tilde(p.path), project: p.project, error: p.error, items: p.backups }))];
  const all = groups.flatMap((g) => g.items.map((b) => ({ b, g })));
  let cur = all.find((x) => x.b.id === ctx.arg) || all[0];

  const draw = () => {
    settle(list);
    mount(list,
      h("div", { class: "listhead" }, h("span", { class: "t-meta", text: t("backups.count", { n: all.length }) }), h("span", { class: "grow" }), h("span", { class: "t-meta", text: t("backups.newest") })),
      groups.map((g) => h("div", { class: "group" },
        h("div", { class: "gh" }, h("h2", { path: g.label }), h("span", { class: "cnt", text: String(g.items.length) })),
        g.error ? notice("bad", t("backups.unreadable"), g.error) : null,
        h("ul", { class: "rows" }, g.items.length ? g.items.map((b) => h("li", {},
          h("button", { class: "rowbtn", type: "button", "aria-pressed": String(cur && cur.b === b), onclick: () => { cur = all.find((x) => x.b === b); draw(); } },
            h("span", { class: "main" },
              h("span", { class: "title" }, h("b", { text: title(b, g) }), badge(b) ? h("span", { class: "pill sm state", text: badge(b) }) : null),
              subtitle(b) ? h("span", { class: "t-meta", text: subtitle(b) }) : null),
            h("span", { class: "when", title: fmtDate(b.createdAt), text: fmtRelative(b.createdAt) }))))
          : h("li", { class: "help empty", text: t("backups.noneHere") })))));
    show(detail, cur, ctx);
  };
  draw();
}

const named = (b) => b.pack && b.pack.name;
const opName = (op) => t("backups.op." + op, {}, op);

// title says what the backup is of: the install of a pack, or the apply,
// restore or fix in a project, by the project's folder name.
function title(b, g) {
  if (named(b)) return t("backups.titleNamed", { op: opName(b.operation), name: b.pack.name });
  if (g.project) return t("backups.titleIn", { op: opName(b.operation), name: g.project.split(/[\\/]/).filter(Boolean).pop() });
  return opName(b.operation);
}

// subtitle is the variant and the mode of a home backup.
function subtitle(b) {
  return [b.variant, b.mode ? lower(t("mode." + b.mode)) : ""].filter(Boolean).join(" · ");
}

function badge(b) {
  if (!b.valid) return t("backups.invalid");
  if (b.head) return t("backups.current");
  if (b.restored) return t("backups.restored");
  return "";
}

function show(detail, cur, ctx) {
  if (!cur) {
    mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: t("backups.emptyTitle") }),
      h("p", { class: "body", text: t("backups.emptyBody") }),
      actions([h("a", { class: "btn", href: "#/packs", text: t("tree.goPacks") })])));
    return;
  }
  const { b, g } = cur;
  const action = h("div", { class: "stack" });
  mount(detail,
    h("div", { class: "col intro" },
      h("p", { class: "meta" }, h("span", { class: "pill state", text: opName(b.operation) }), badge(b) ? h("span", { class: "pill state", text: badge(b) }) : null,
        h("span", { class: "t-meta", title: fmtDate(b.createdAt), text: fmtRelative(b.createdAt) })),
      h("h2", { class: "h-detail", text: headline(b, g) }),
      b.head ? hint(t("backups.currentHint")) : null,
      b.error ? notice("bad", t("backups.invalidBody"), b.error) : null),
    h("div", { class: "col" },
      sec(t("backups.details"), "", kv([
        [t("backups.k.created"), fmtDate(b.createdAt)],
        [t("backups.k.pack"), named(b) ? b.pack.name + " " + b.pack.version : ""],
        [t("backups.k.variant"), b.variant],
        [t("backups.k.mode"), b.mode ? t("mode." + b.mode) : ""],
        [t("backups.k.harnesses"), (b.harnesses || []).map(harnessName).join(", ")],
        [t("backups.k.project"), g.project ? tilde(g.project) : "", true],
        [t("backups.k.id"), b.id, true],
        [t("backups.k.directory"), tilde(b.path), true],
      ])),
      sec(t("backups.files", { n: b.targets.length }), t("backups.filesHint"), b.targets.length ? h("ul", { class: "paths" }, b.targets.map((x) => h("li", { class: "t-path ink", text: tilde(x) }))) : h("p", { class: "quietp", text: t("backups.noTargets") })),
      sec(t("backups.restoreSec"), "", action)));
  if (g.scope === "home") {
    offer(action, t("backups.restoreHome"), t("backups.preview"), restoreHomeFlow(b.id, ctx));
  } else if (g.project) {
    offer(action, t("backups.restoreProject"), t("repos.restorePreview"), restoreProjectFlow(g.project, ctx));
  } else {
    mount(action, h("p", { class: "body", text: t("backups.cannotRestore") }));
  }
}

function offer(el, text, label, flow) {
  mount(el, h("p", { class: "body", text }),
    actions([h("button", { class: "btn primary", type: "button", onclick: () => confirmFlow(el, flow), text: label })], t("backups.previewNote")));
}

function headline(b, g) {
  if (b.operation === "install" && named(b)) return t(b.mode === "replace" ? "backups.headReplace" : "backups.headInstall", { name: b.pack.name, version: b.pack.version });
  if (g.project) return t("backups.headIn", { op: opName(b.operation), path: tilde(g.project) });
  return opName(b.operation);
}
