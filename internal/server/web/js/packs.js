// The Packs board: the recent packs on the left, the selected one on the
// right, as dotai validate reads its manifest.

import { h, icon, lm, mount, notice, sec, when } from "./dom.js";
import { forgetPack, harnessName, installed, isAbsolute, isInstalled, loadPack, recentPacks, rememberPack, selectPack, selectedPack, tilde } from "./store.js";

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-labelledby": "packs-h" });
  const detail = h("section", { class: "detail", "aria-label": "Selected pack" });
  mount(root, list, detail);
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
      h("h1", { class: "h-list", id: "packs-h", text: "Packs" }),
      h("span", { class: "t-cap", text: paths.length + " known" }),
      h("span", { class: "grow" }),
      h("button", { class: "btn quiet sm", type: "button", onclick: () => { form.hidden = !form.hidden; form.querySelector("input").focus(); } }, icon("plus", 16), "Add pack")),
    form, rows);

  if (paths.length === 0) {
    mount(detail, h("div", { class: "col intro" },
      h("h2", { class: "h-detail", text: "No pack yet" }),
      h("p", { class: "lead", text: "Add the absolute path of a pack directory, the one that holds dotai.pack.yaml. This browser remembers the packs you add; dotai keeps no list of them yet." })));
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
  const input = h("input", { class: "input", type: "text", name: "pack", placeholder: "/path/to/pack", "aria-label": "Absolute path of a pack", autocomplete: "off", spellcheck: "false" });
  const err = h("p", { class: "t-cap", role: "alert" });
  return h("form", { class: "field empty", onsubmit: (e) => {
    e.preventDefault();
    const v = input.value.trim();
    if (!isAbsolute(v)) {
      err.textContent = "Give an absolute path: dotai ui has no current directory of yours to start from.";
      return;
    }
    add(v);
  } }, h("div", { class: "row" }, input, h("button", { class: "btn", type: "submit", text: "Add" })), err);
}

function packRow(path, res, pressed, pick) {
  const pack = res.data && res.data.pack;
  const on = isInstalled(path);
  return h("li", {}, h("button", { class: "rowbtn tall", type: "button", "aria-pressed": String(pressed), onclick: pick },
    h("span", { class: "lead-dot" }, h("span", { class: on ? "dot on" : "dot" })),
    h("span", { class: "main" },
      h("span", { class: "title" }, h("b", { text: pack ? pack.name : "unreadable pack" }), pack ? h("span", { class: "t-cap", text: pack.version }) : null),
      h("span", { class: "t-path", text: tilde(path) }),
      pack ? h("span", { class: "pills" }, [...pack.harnesses.map((id) => h("span", { class: "pill sm", text: id })),
        ...pack.optionalHarnesses.map((id) => h("span", { class: "pill sm", text: id + " (optional)" }))]) : null),
    h("span", {}, on ? h("span", { class: "pill sm you", text: "installed" })
      : res.ok ? h("span", { class: "t-cap", text: "not installed" }) : h("span", { class: "pill sm warn", text: res.error.code }))));
}

function showPack(detail, path, res, forget) {
  const pack = res.data && res.data.pack;
  const on = isInstalled(path);
  const s = installed();
  const enc = encodeURIComponent(path);
  if (!pack) {
    mount(detail, h("div", { class: "col intro" },
      h("p", { class: "t-path", text: tilde(path) }),
      h("h2", { class: "h-detail", text: "dotai cannot read this pack" }),
      notice("bad", res.error.code, res.error.message),
      h("div", { class: "row" }, h("button", { class: "btn quiet", type: "button", onclick: forget, text: "Forget this path" }))));
    return;
  }
  const intro = h("div", { class: "col intro" },
    h("div", { class: "row" },
      h("span", { class: on ? "dot on" : "dot" }),
      h("span", { class: "t-cap", text: "dotai.pack.yaml" }),
      on ? h("span", { class: "pill you", text: "installed · " + s.mode }) : h("span", { class: "pill state", text: "not installed" }),
      on ? h("span", { class: "t-cap", text: "variant " + s.variant + " · since " + when(s.installedAt) }) : null),
    h("h2", { class: "h-page" }, pack.name + " ", h("span", { class: "ver", text: pack.version })),
    h("p", { class: "t-cap caps" }, h("span", { text: tilde(path) }), h("span", { text: "engine " + pack.engine }), h("span", { text: "rulesync " + pack.rulesync })),
    h("div", { class: "row" },
      h("a", { class: "btn primary", href: "#/plan/" + enc, text: "Plan install" }),
      h("a", { class: "btn", href: "#/tree/" + enc, text: "Merged tree" }),
      pack.profiles.length ? h("a", { class: "btn", href: "#/projects", text: "Apply to a project" }) : null,
      h("button", { class: "btn quiet", type: "button", onclick: forget, text: "Forget" })),
    h("p", { class: "t-cap", text: !on && s ? `Installing ${pack.name} replaces ${s.pack.name} as the user-level install.`
      : "Plan install lists every file it would write; nothing is written until you confirm." }));

  const warnings = res.ok ? res.data.warnings : res.data.diagnostics;
  const layers = (kind) => pack.layers.filter((l) => l.kind === kind);
  const base = layers("base")[0];
  mount(detail, intro, h("div", { class: "col" },
    !res.ok ? notice("bad", res.error.code, res.error.message) : null,
    sec("Harnesses", [...pack.harnesses.map((id) => harnessRow(id, "required")), ...pack.optionalHarnesses.map((id) => harnessRow(id, "optional"))]),
    base ? sec("Base", h("div", { class: "fl" }, lm("base"), h("span", { class: "text", text: "Always applied, at user level." }), h("span", { class: "t-cap full", text: items(base.items) }))) : null,
    pack.variants.length ? sec("Variants", pack.variants.map((v) => {
      const l = layers("variant").find((x) => x.name === v);
      return h("div", { class: "fl" }, lm(v),
        v === pack.defaultVariant ? h("span", { class: "pill sm state", text: "default" }) : null,
        on && s.variant === v ? h("span", { class: "pill sm you", text: "installed" }) : null,
        pack.sharedSkills.includes(v) ? h("span", { class: "t-cap", text: "shared-skills: true" }) : null,
        l ? h("span", { class: "t-cap full", text: items(l.items) + " · " + l.path }) : null);
    })) : null,
    pack.profiles.length ? sec("Profiles", pack.profiles.map((p) => h("div", { class: "fl" }, lm(p.name),
      h("span", { class: "text", text: p.description || "" }),
      h("span", { class: "t-cap full", text: [p.hints.length ? "hints " + p.hints.join(", ") : "", p.mods.length ? "mods " + p.mods.join(", ") : "", "project scope, applied with dotai apply --profile " + p.name].filter(Boolean).join(" · ") })))) : null,
    mods(pack),
    warnings && warnings.length ? sec(res.ok ? "Warnings" : "Problems", h("ul", {}, warnings.map((d) => h("li", { class: "wn" }, icon("warn", 16),
      h("span", {}, h("span", { class: "code", text: d.code }), " ", h("span", { class: "t-cap", text: d.file ? d.file + (d.line ? ":" + d.line : "") : "" }), h("br"), h("span", { class: "msg", text: d.message })))))) : null));
}

function harnessRow(id, need) {
  return h("div", { class: "fl" }, h("span", { class: "name", text: harnessName(id) }), h("span", { class: "id", text: id }), h("span", { class: "pill sm state", text: need }));
}

function items(n) {
  return n === 1 ? "1 item" : n + " items";
}

function mods(pack) {
  const byMod = new Map();
  for (const p of pack.profiles) for (const m of p.mods) byMod.set(m, [...(byMod.get(m) || []), p.name]);
  if (byMod.size === 0) return null;
  return sec("Mods", [...byMod].map(([m, by]) => h("div", { class: "fl" }, h("span", { class: "mono", text: m }), h("span", { class: "t-cap full", text: "named by " + by.join(", ") + " · installs in a project" }))));
}
