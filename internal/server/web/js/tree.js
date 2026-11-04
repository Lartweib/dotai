// The Merged tree board: what an install of the pack writes into each
// harness, as its plan lists it, grouped by kind. The plan is the merged
// result: dotai stages the pack, merges its layers and renders it with
// rulesync. It names each target and the staged file it comes from, but
// not the layer that gave it, so the board shows the target, the source
// and the keys of a settings file, and no layer chain. The kind of each
// target, which sorts it into a group, comes with the plan.

import { busy, h, icon, mount, notice, sec } from "./dom.js";
import { rel, word } from "./flows.js";
import { harnessName, installed, isInstalled, loadPack, post, selectedPack, tilde } from "./store.js";

const groups = [
  ["rules", "Rules", "doc"],
  ["skills", "Skills", "sparkle"],
  ["agents", "Agents", "robot"],
  ["commands", "Commands", "doc"],
  ["settings", "Settings", "gear"],
  ["tools", "Tools", "wrench"],
  ["other", "Other files", "file"],
];

export async function render(root, ctx) {
  const s = installed();
  const pack = ctx.arg || (s ? s.pack.path : selectedPack());
  const pane = h("section", { class: "treepane", "aria-labelledby": "tree-h" });
  const detail = h("section", { class: "detail", "aria-label": "Selected item" });
  if (!pack) {
    root.className = "view single";
    mount(root, h("div", { class: "col" }, notice("", "", "Nothing is installed and no pack is known yet. Add one on the Packs board.")));
    return;
  }
  mount(root, pane, detail);
  mount(pane, busy("Merging the pack: dotai stages it and runs rulesync, nothing is written..."));
  const on = isInstalled(pack) && s;
  const [info, res] = await Promise.all([loadPack(pack), post("/api/install/plan", { pack, mode: on ? s.mode : "", variant: on ? s.variant : "" }, ctx.signal)]);
  if (ctx.stale()) return;
  const summary = info.data && info.data.pack;
  if (!res.data || !res.data.ops) {
    mount(pane, notice("bad", res.error.code, res.error.message));
    return;
  }
  const p = res.data;
  const tabs = p.harnesses.filter((x) => x.managed);
  const view = { harness: tabs[0] ? tabs[0].id : "", sel: "", closed: new Set() };

  const draw = () => {
    const hp = tabs.find((x) => x.id === view.harness);
    const dir = hp ? hp.dir : p.root;
    const ops = p.ops.filter((o) => o.harness === view.harness || !o.harness);
    const items = ops.map((o) => {
      const path = o.harness ? rel(dir, o.target) : tilde(o.target);
      return { op: o, path, group: groups.some((g) => g[0] === o.kind) ? o.kind : "other", id: o.target };
    });
    const cur = items.find((it) => it.id === view.sel) || items[0];
    mount(pane,
      h("div", { class: "intro tight" },
        h("div", { class: "crumbs" }, h("a", { href: "#/packs", text: summary ? summary.name + " " + summary.version : tilde(pack) }),
          h("span", { class: "t-cap", "aria-hidden": "true", text: "/" }), h("h1", { class: "h-list", id: "tree-h", text: "Merged tree" })),
        h("p", { class: "t-cap", text: ["user scope", "base + " + (p.variant || "no variant"), p.mode, on ? "installed now: " + s.variant : "not installed"].join(" · ") }),
        h("div", { class: "tabs", role: "tablist", "aria-label": "Harness" }, tabs.map((t) =>
          h("button", { class: "btn sm", type: "button", role: "tab", "aria-selected": String(t.id === view.harness), text: harnessName(t.id),
            onclick: () => { view.harness = t.id; view.sel = ""; draw(); } }))),
        h("p", { class: "t-path", text: tilde(dir) })),
      h("ul", { "aria-label": "What installs into " + harnessName(view.harness) }, groups.map(([gid, label, ic]) => {
        const kids = items.filter((it) => it.group === gid);
        if (kids.length === 0 && (gid === "other" || gid === "commands")) return null;
        const open = !view.closed.has(gid);
        return h("li", {},
          h("button", { class: "grp", type: "button", "aria-expanded": String(open), onclick: () => { open ? view.closed.add(gid) : view.closed.delete(gid); draw(); } },
            h("span", { class: "chev" }, icon("chev", 16)), h("span", { class: "ico" }, icon(ic, 18)), h("span", { text: label }), h("span", { class: "cnt", text: String(kids.length) })),
          open ? h("ul", { class: "kids" }, kids.length ? kids.map((it) =>
            h("li", {}, h("button", { class: "leaf", type: "button", "aria-pressed": String(it === cur), onclick: () => { view.sel = it.id; draw(); } },
              h("span", { class: "main" }, h("span", { class: "nm", text: leafName(it) }), h("span", { class: "sub", text: it.path })),
              it.op.action !== "unchanged" ? h("span", { class: "tag", "data-a": word(it.op.action), text: word(it.op.action) }) : null)))
            : h("li", { class: "t-cap empty", text: "None in this pack for " + harnessName(view.harness) + "." })) : null);
      })));
    showItem(detail, cur, view.harness);
  };
  draw();
}

function leafName(it) {
  const parts = it.path.split("/");
  const last = parts[parts.length - 1];
  if (/^skill\.md$/i.test(last) && parts.length > 1) return parts[parts.length - 2];
  return last;
}

function showItem(detail, it, harness) {
  if (!it) {
    mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: "Nothing to install" }),
      h("p", { class: "lead", text: "The plan writes no file for " + harnessName(harness) + "." })));
    return;
  }
  const o = it.op;
  mount(detail,
    h("div", { class: "col intro tight" },
      h("div", { class: "row" }, h("span", { class: "t-cap", text: groups.find((g) => g[0] === it.group)[1] }),
        h("span", { class: "pill sm state", text: word(o.action) }), h("span", { class: "t-cap", text: harnessName(o.harness) })),
      h("h2", { class: "h-detail", text: leafName(it) }),
      h("p", { class: "lead", text: "The install plan says " + word(o.action) + " for this file" + (o.harness ? ", for " + harnessName(o.harness) : ", shared by every harness of the user scope") + "." })),
    h("div", { class: "col" },
      sec("Writes to", h("div", { class: "sc stack" }, h("p", { class: "mono t-path", text: o.target }), h("p", { class: "t-cap", text: o.exec ? "executable" : "" }))),
      o.keys && o.keys.length ? sec("Keys", h("ul", {}, o.keys.map((k) => h("li", { class: "op" }, h("span", { class: "tag", "data-a": word(k.action), text: word(k.action) }), h("span", { class: "p", text: k.key }))))) : null,
      sec("Source", h("div", { class: "sc stack" }, h("p", { class: "t-cap", text: "The staged file, merged from the pack's layers and rendered by rulesync." }), h("pre", { class: "src", text: o.source || "(none)" }))),
      o.link ? sec("Link", h("pre", { class: "src", text: o.link })) : null));
}
