// The Merged tree board: what an install of the pack writes into each
// harness, as its plan lists it, grouped by kind. The plan is the merged
// result: dotai stages the pack, merges its layers and renders it with
// rulesync. It names each target and the staged file it comes from, but
// not the layer that gave it, so the board shows the target, the source
// and the keys of a settings file, and no layer chain. The kind of each
// target, which sorts it into a group, comes with the plan.

import { boardHead, busy, failure, h, icon, mount, sec } from "./dom.js";
import { rel, word } from "./flows.js";
import { t } from "./i18n.js";
import { harnessName, installed, isInstalled, loadPack, post, selectedPack, tilde } from "./store.js";

const groups = [
  ["rules", "doc"],
  ["skills", "sparkle"],
  ["agents", "robot"],
  ["commands", "doc"],
  ["settings", "gear"],
  ["tools", "wrench"],
  ["other", "file"],
];

const groupName = (id) => t("kind." + id);

export async function render(root, ctx) {
  const s = installed();
  const pack = ctx.arg || (s ? s.pack.path : selectedPack());
  const pane = h("section", { class: "treepane", "aria-label": t("tree.listLabel") });
  const detail = h("section", { class: "detail", "aria-label": t("tree.detailLabel") });
  if (!pack) {
    mount(root, boardHead("tree"), h("div", { class: "detail solo" }, h("div", { class: "col intro" },
      h("h2", { class: "h-detail", text: t("tree.emptyTitle") }),
      h("p", { class: "body", text: t("tree.emptyBody") }),
      h("div", { class: "actions" }, h("div", { class: "btns" }, h("a", { class: "btn primary", href: "#/packs", text: t("tree.goPacks") }))))));
    return;
  }
  mount(root, boardHead("tree"), pane, detail);
  mount(pane, busy(t("tree.merging")));
  const on = isInstalled(pack) && s;
  const [info, res] = await Promise.all([loadPack(pack), post("/api/install/plan", { pack, mode: on ? s.mode : "", variant: on ? s.variant : "" }, ctx.signal)]);
  if (ctx.stale()) return;
  const summary = info.data && info.data.pack;
  if (!res.data || !res.data.ops) {
    mount(pane, failure(res.error));
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
      h("div", { class: "listhead stack" },
        h("p", { class: "meta" }, h("a", { class: "t-meta", href: "#/packs", text: summary ? summary.name + " " + summary.version : tilde(pack) }),
          h("span", { class: on ? "pill sm you" : "pill sm state", text: on ? t("tree.installedNow", { variant: s.variant }) : t("tree.notInstalled") })),
        h("p", { class: "help", text: t("tree.summary", { variant: p.variant || t("tree.noVariant"), mode: t("mode." + p.mode) }) }),
        h("div", { class: "tabs", role: "tablist", "aria-label": t("tree.harness") }, tabs.map((x) =>
          h("button", { class: "btn sm", type: "button", role: "tab", "aria-selected": String(x.id === view.harness), text: harnessName(x.id),
            onclick: () => { view.harness = x.id; view.sel = ""; draw(); } }))),
        h("p", { class: "t-path", text: tilde(dir) })),
      h("ul", { "aria-label": t("tree.intoLabel", { name: harnessName(view.harness) }) }, groups.map(([gid, ic]) => {
        const kids = items.filter((it) => it.group === gid);
        if (kids.length === 0 && (gid === "other" || gid === "commands")) return null;
        const open = !view.closed.has(gid);
        return h("li", {},
          h("button", { class: "grp", type: "button", "aria-expanded": String(open), onclick: () => { open ? view.closed.add(gid) : view.closed.delete(gid); draw(); } },
            h("span", { class: "chev" }, icon("chev", 16)), h("span", { class: "ico" }, icon(ic, 18)), h("span", { text: groupName(gid) }), h("span", { class: "cnt", text: String(kids.length) })),
          open ? h("ul", { class: "kids" }, kids.length ? kids.map((it) =>
            h("li", {}, h("button", { class: "leaf", type: "button", "aria-pressed": String(it === cur), onclick: () => { view.sel = it.id; draw(); } },
              h("span", { class: "main" }, h("span", { class: "nm", text: leafName(it) }), h("span", { class: "sub", text: it.path })),
              it.op.action !== "unchanged" ? h("span", { class: "tag", "data-a": it.op.action, text: word(it.op.action) }) : null)))
            : h("li", { class: "help empty", text: t("tree.none", { name: harnessName(view.harness) }) })) : null);
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
    mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: t("tree.nothingTitle") }),
      h("p", { class: "body", text: t("tree.nothingBody", { name: harnessName(harness) }) })));
    return;
  }
  const o = it.op;
  mount(detail,
    h("div", { class: "col intro tight" },
      h("div", { class: "meta" }, h("span", { class: "t-meta", text: groupName(it.group) }),
        h("span", { class: "tag", "data-a": o.action, text: word(o.action) }), h("span", { class: "t-meta", text: harnessName(o.harness) })),
      h("h2", { class: "h-detail", text: leafName(it) }),
      h("p", { class: "body", text: o.harness ? t("tree.itemLead", { action: word(o.action), name: harnessName(o.harness) }) : t("tree.itemLeadShared", { action: word(o.action) }) })),
    h("div", { class: "col" },
      sec(t("tree.writesTo"), "", h("p", { class: "t-path ink", text: o.target }), o.exec ? h("p", { class: "help", text: t("tree.executable") }) : null),
      o.keys && o.keys.length ? sec(t("tree.keys"), t("hint.keys"), h("ul", { class: "list" }, o.keys.map((k) => h("li", { class: "op" }, h("span", { class: "tag", "data-a": k.action, text: word(k.action) }), h("span", { class: "p", text: k.key }))))) : null,
      sec(t("tree.source"), t("tree.sourceHint"), h("pre", { class: "src", text: o.source || t("tree.noSource") })),
      o.link ? sec(t("tree.link"), "", h("pre", { class: "src", text: o.link })) : null));
}
