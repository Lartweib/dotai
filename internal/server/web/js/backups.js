// The Backups board: every backup, newest first, the home ones and those
// of each project. A home backup restores through a preview first: the
// restore carries out only the preview it shows, by its digest, and the
// server refuses what cannot be restored. A project is restored as a
// whole, through the preview of restore --project.

import { busy, h, kv, mount, notice, op, sec, when } from "./dom.js";
import { confirmFlow, restoreHomeFlow, restoreProjectFlow } from "./flows.js";
import { api, tilde } from "./store.js";

const titles = { install: "Install", restore: "Restore", apply: "Apply", fix: "Fix exclude entries" };

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-labelledby": "bk-h" });
  const detail = h("section", { class: "detail", "aria-label": "Selected backup" });
  mount(root, list, detail);
  mount(list, busy("Reading the backups..."));
  const res = await api("/api/backups", { signal: ctx.signal });
  if (ctx.stale()) return;
  if (!res.ok) {
    mount(list, notice("bad", res.error.code, res.error.message));
    return;
  }
  const groups = [{ scope: "home", label: "Home", dir: "", project: "", items: res.data.home },
    ...res.data.projects.map((p) => ({ scope: "project", label: p.project ? tilde(p.project) : "unknown project", dir: tilde(p.path), project: p.project, error: p.error, items: p.backups }))];
  const all = groups.flatMap((g) => g.items.map((b) => ({ b, g })));
  ctx.setCount("backups", all.length);
  let cur = all.find((x) => x.b.id === ctx.arg) || all[0];

  const draw = () => {
    mount(list,
      h("div", { class: "listhead" }, h("h1", { class: "h-list", id: "bk-h", text: "Backups" }), h("span", { class: "t-cap", text: "newest first" })),
      groups.map((g) => h("div", {},
        h("div", { class: "gh" }, h("h2", { text: g.label }), h("span", { class: "t-cap", text: String(g.items.length) }), g.dir ? h("span", { class: "t-cap dir", text: g.dir }) : null),
        g.error ? notice("bad", "unreadable", g.error) : null,
        h("ul", { class: "rows" }, g.items.length ? g.items.map((b) => h("li", {},
          h("button", { class: "rowbtn", type: "button", "aria-pressed": String(cur && cur.b === b), onclick: () => { cur = all.find((x) => x.b === b); draw(); } },
            h("span", { class: "main" },
              h("span", { class: "title" }, h("b", { text: title(b) }), badge(b) ? h("span", { class: "pill sm state", text: badge(b) }) : null),
              h("span", { class: "t-path", text: b.id })),
            h("span", { class: "when", text: when(b.createdAt) }))))
          : h("li", { class: "t-cap empty", text: "No backup yet." })))));
    show(detail, cur, ctx);
  };
  draw();
}

const named = (b) => b.pack && b.pack.name;

function title(b) {
  const what = titles[b.operation] || b.operation;
  return named(b) ? what + " " + b.pack.name : what;
}

function badge(b) {
  if (!b.valid) return "invalid";
  if (b.head) return "current";
  if (b.restored) return "restored";
  return "";
}

function show(detail, cur, ctx) {
  if (!cur) {
    mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: "No backups" }),
      h("p", { class: "lead", text: "An install, an apply, a restore and doctor --fix each take a backup before they write. None has run here yet." })));
    return;
  }
  const { b, g } = cur;
  const action = h("div", { class: "sc stack" });
  mount(detail,
    h("div", { class: "col intro tight" },
      h("div", { class: "row" }, h("span", { class: "t-path", text: b.id }), h("span", { class: "pill state", text: b.operation }), badge(b) ? h("span", { class: "pill state", text: badge(b) }) : null),
      h("h2", { class: "h-detail", text: headline(b, g) }),
      b.error ? notice("bad", "invalid", b.error) : null),
    h("div", { class: "col" },
      sec("Covers", kv([
        ["created", new Date(b.createdAt).toUTCString()],
        ["pack", named(b) ? b.pack.name + " " + b.pack.version + ", " + tilde(b.pack.path) : ""],
        ["variant", b.variant],
        ["mode", b.mode],
        ["harnesses", (b.harnesses || []).join(", ")],
        ["project", g.project ? tilde(g.project) : ""],
        ["directory", tilde(b.path)],
      ])),
      sec("Changes", b.targets.length ? h("ul", {}, b.targets.map((t) => op("covers", tilde(t)))) : h("p", { class: "quietp", text: "The manifest lists no target." })),
      h("div", { class: "sec" }, h("h2", { text: "Restore" }), action)));
  if (g.scope === "home") {
    offer(action, "A restore puts the files of this install back as they were before it. It shows what it writes first.", "Preview restore", restoreHomeFlow(b.id, ctx));
  } else if (g.project) {
    offer(action, "A project is restored as a whole: dotai restore --project undoes every apply recorded there.", "Preview restore of the project", restoreProjectFlow(g.project, ctx));
  } else {
    mount(action, h("p", { class: "body", text: "The project of this backup is unknown, so it cannot be restored from here." }));
  }
}

function offer(el, text, label, flow) {
  mount(el, h("p", { class: "body", text }),
    h("div", { class: "row" }, h("button", { class: "btn primary", type: "button", onclick: () => confirmFlow(el, flow), text: label })));
}

function headline(b, g) {
  const what = titles[b.operation] || b.operation;
  if (b.operation === "install" && named(b)) return (b.mode === "replace" ? "Replace install" : "Install") + " of " + b.pack.name + " " + b.pack.version + (b.mode ? ", " + b.mode : "");
  if (g.project) return what + " in " + tilde(g.project);
  return what;
}
