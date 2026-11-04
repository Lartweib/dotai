// The Packs board: the recent packs on the left, the selected one on the
// right, as dotai validate reads its manifest.

import { actions, boardHead, failure, h, hint, icon, lm, mount, sec } from "./dom.js";
import { fmtDate, t } from "./i18n.js";
import { forgetPack, harnessName, installed, isAbsolute, isInstalled, loadPack, recentPacks, rememberPack, selectPack, selectedPack, tilde } from "./store.js";

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-label": t("packs.listLabel") });
  const detail = h("section", { class: "detail", "aria-label": t("packs.detailLabel") });
  mount(root, boardHead("packs"), list, detail);
  const paths = recentPacks();
  ctx.setCount("packs", paths.length);
  let current = selectedPack();

  const form = addForm((path) => {
    rememberPack(path);
    location.hash = "#/packs";
    render(root, ctx);
  });
  form.hidden = paths.length > 0;

  const rows = h("ul", { class: "rows gap" });
  mount(list,
    h("div", { class: "listhead" },
      h("span", { class: "t-meta", text: t("packs.known", { n: paths.length }) }),
      h("span", { class: "grow" }),
      paths.length ? h("button", { class: "btn quiet sm", type: "button", "aria-expanded": "false", onclick: (e) => {
        form.hidden = !form.hidden;
        e.currentTarget.setAttribute("aria-expanded", String(!form.hidden));
        if (!form.hidden) form.querySelector("input").focus();
      } }, icon("plus", 16), t("packs.add")) : null),
    form, rows);

  if (paths.length === 0) {
    mount(detail, h("div", { class: "col intro" },
      h("h2", { class: "h-detail", text: t("packs.emptyTitle") }),
      h("p", { class: "body", text: t("packs.emptyBody") }),
      hint(t("packs.emptyHint"))));
    return;
  }

  const results = await Promise.all(paths.map((p) => loadPack(p)));
  if (ctx.stale()) return;
  const draw = () => {
    mount(rows, paths.map((p, i) => packRow(p, results[i], p === current, () => {
      current = p;
      selectPack(p);
      draw();
    })));
    showPack(detail, current, results[paths.indexOf(current)], () => {
      forgetPack(current);
      render(root, ctx);
    });
  };
  if (!paths.includes(current)) current = paths[0];
  draw();
}

function addForm(add) {
  const input = h("input", { class: "input mono", id: "pack-path", type: "text", name: "pack", placeholder: t("packs.placeholder"), autocomplete: "off", spellcheck: "false", "aria-describedby": "pack-help" });
  const err = h("p", { class: "err", role: "alert" });
  return h("form", { class: "form addform", onsubmit: (e) => {
    e.preventDefault();
    const v = input.value.trim();
    if (!isAbsolute(v)) {
      err.textContent = t("packs.notAbsolute");
      input.setAttribute("aria-invalid", "true");
      return;
    }
    add(v);
  } },
  h("div", { class: "field" },
    h("label", { class: "label", for: "pack-path", text: t("packs.pathLabel") }),
    h("div", { class: "inline" }, input, h("button", { class: "btn", type: "submit", text: t("packs.addBtn") })),
    h("p", { class: "help", id: "pack-help", text: t("packs.pathHelp") }), err));
}

function packRow(path, res, pressed, pick) {
  const pack = res.data && res.data.pack;
  const on = isInstalled(path);
  return h("li", {}, h("button", { class: "rowbtn tall", type: "button", "aria-pressed": String(pressed), onclick: pick },
    h("span", { class: "lead-dot" }, h("span", { class: on ? "dot on" : "dot" })),
    h("span", { class: "main" },
      h("span", { class: "title" }, h("b", { text: pack ? pack.name : t("packs.unreadable") }), pack ? h("span", { class: "t-meta mono", text: pack.version }) : null),
      h("span", { class: "t-path", text: tilde(path) })),
    h("span", { class: "side-mark" }, on ? h("span", { class: "pill sm you", text: t("packs.installed") })
      : res.ok ? h("span", { class: "t-meta", text: t("packs.notInstalled") }) : h("span", { class: "pill sm warn", text: res.error.code }))));
}

function showPack(detail, path, res, forget) {
  const pack = res.data && res.data.pack;
  const on = isInstalled(path);
  const s = installed();
  const enc = encodeURIComponent(path);
  if (!pack) {
    mount(detail, h("div", { class: "col intro" },
      h("p", { class: "t-path", text: tilde(path) }),
      h("h2", { class: "h-detail", text: t("packs.cannotRead") }),
      failure(res.error),
      actions([h("button", { class: "btn", type: "button", onclick: forget, text: t("packs.forgetPath") })])));
    return;
  }
  const intro = h("div", { class: "col intro" },
    h("div", { class: "meta" },
      on ? h("span", { class: "pill you", text: t("packs.installedMode", { mode: t("mode." + s.mode) }) }) : h("span", { class: "pill state", text: t("packs.notInstalled") }),
      on ? h("span", { class: "t-meta", text: t("packs.installedSince", { variant: s.variant, date: fmtDate(s.installedAt) }) }) : null),
    h("h2", { class: "h-page" }, pack.name + " ", h("span", { class: "ver", text: pack.version })),
    h("p", { class: "caps" }, h("span", { class: "t-path", text: tilde(path) }),
      h("span", { class: "t-meta", text: t("packs.engine", { v: pack.engine }) }), h("span", { class: "t-meta", text: t("packs.rulesync", { v: pack.rulesync }) })),
    actions([
      h("a", { class: "btn primary", href: "#/plan/" + enc, text: t(on ? "packs.planReinstall" : "packs.planInstall") }),
      h("a", { class: "btn", href: "#/tree/" + enc, text: t("packs.seeFiles") }),
      pack.profiles.length ? h("a", { class: "btn", href: "#/projects", text: t("packs.applyProject") }) : null,
      h("button", { class: "btn quiet", type: "button", onclick: forget, text: t("packs.forget") }),
    ].filter(Boolean), !on && s ? t("packs.replacesNote", { name: pack.name, other: s.pack.name }) : t("packs.planNote")));

  const warnings = res.ok ? res.data.warnings : res.data.diagnostics;
  const layers = (kind) => pack.layers.filter((l) => l.kind === kind);
  const base = layers("base")[0];
  mount(detail, intro, h("div", { class: "col" },
    !res.ok ? failure(res.error) : null,
    sec(t("packs.harnesses"), t("hint.harness"), h("ul", { class: "fls" }, [...pack.harnesses.map((id) => harnessRow(id, "required")), ...pack.optionalHarnesses.map((id) => harnessRow(id, "optional"))])),
    base ? sec(t("packs.base"), t("hint.base"), h("ul", { class: "fls" }, h("li", { class: "fl" }, lm("base"), h("span", { class: "t-meta", text: t("common.items", { n: base.items }) })))) : null,
    pack.variants.length ? sec(t("packs.variants"), t("hint.variant"), h("ul", { class: "fls" }, pack.variants.map((v) => {
      const l = layers("variant").find((x) => x.name === v);
      return h("li", { class: "fl" }, lm(v),
        v === pack.defaultVariant ? h("span", { class: "pill sm state", text: t("packs.default") }) : null,
        on && s.variant === v ? h("span", { class: "pill sm you", text: t("packs.installed") }) : null,
        pack.sharedSkills.includes(v) ? h("span", { class: "t-meta", text: t("packs.sharedSkills") }) : null,
        l ? h("span", { class: "t-meta end", text: t("common.items", { n: l.items }) }) : null);
    }))) : null,
    pack.profiles.length ? sec(t("packs.profiles"), t("hint.profile"), h("ul", { class: "fls" }, pack.profiles.map((p) => h("li", { class: "fl" }, lm(p.name),
      h("span", { class: "text", text: p.description || "" }),
      h("span", { class: "t-meta full", text: [p.hints.length ? t("packs.hints", { list: p.hints.join(", ") }) : "", p.mods.length ? t("packs.mods", { list: p.mods.join(", ") }) : ""].filter(Boolean).join(" · ") }))))) : null,
    mods(pack),
    warnings && warnings.length ? sec(t(res.ok ? "packs.warnings" : "packs.problems"), "", h("ul", { class: "list" }, warnings.map((d) => h("li", { class: "wn" }, icon("warn", 16),
      h("span", { class: "wt" }, h("span", { class: "code", text: d.code + (d.file ? " · " + d.file + (d.line ? ":" + d.line : "") : "") }), h("span", { class: "msg", text: d.message })))))) : null));
}

function harnessRow(id, need) {
  return h("li", { class: "fl" }, h("span", { class: "name", text: harnessName(id) }), h("span", { class: "id", text: id }), h("span", { class: "pill sm state end", text: t("packs." + need) }));
}

function mods(pack) {
  const byMod = new Map();
  for (const p of pack.profiles) for (const m of p.mods) byMod.set(m, [...(byMod.get(m) || []), p.name]);
  if (byMod.size === 0) return null;
  return sec(t("packs.modsTitle"), t("hint.mod"), h("ul", { class: "fls" }, [...byMod].map(([m, by]) => h("li", { class: "fl" }, h("span", { class: "name mono", text: m }), h("span", { class: "t-meta", text: t("packs.namedBy", { list: by.join(", ") }) })))));
}
