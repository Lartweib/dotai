// The Packs board: the packs of the registry (~/.dotai/packs.json) on the
// left, the selected one on the right: what each of its layers holds, by
// type, as the server reads it, and what dotai validate says. Import adds a
// folder once it validates, copy makes a new folder to edit, and remove
// only forgets the entry.

import { actions, boardHead, busy, failure, h, hint, icon, kv, lm, mount, notice, sec, tech, warning } from "./dom.js";
import { fmtDate, lower, t } from "./i18n.js";
import { act, harnessName, installed, isAbsolute, isInstalled, loadOutline, loadPack, registry, selectPack, selectedPack, setRegistry, shared, tilde } from "./store.js";

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-label": t("packs.listLabel") });
  const detail = h("section", { class: "detail", "aria-label": t("packs.detailLabel") });
  mount(root, boardHead("packs"), list, detail);
  if (shared.registryError) {
    const e = shared.registryError;
    mount(list, h("div", { class: "listhead stack" }, h("p", { class: "t-meta", text: t("packs.registryBad") })));
    mount(detail, h("div", { class: "col intro" },
      h("h2", { class: "h-detail", text: t("packs.registryBadTitle") }),
      h("p", { class: "t-path ink", text: tilde((e.data && e.data.path) || "~/.dotai/packs.json") }),
      h("p", { class: "body", text: t("packs.registryBadBody") }),
      tech(e.message),
      actions([h("button", { class: "btn primary", type: "button", text: t("common.tryAgain"), onclick: () => ctx.refresh().then(() => ctx.again()) })])));
    return;
  }
  const packs = registry();
  let current = selectedPack();

  const doImport = async (path, err) => {
    const res = await act("/api/packs/import", { path });
    if (res.ok) {
      setRegistry(res.data);
      selectPack(path);
    }
    if (ctx.stale()) return;
    if (!res.ok) {
      mount(err, failure(res.error));
      return;
    }
    ctx.again();
  };

  if (packs.length === 0) {
    mount(list, h("div", { class: "listhead" }, h("span", { class: "t-meta", text: t("packs.none") })));
    mount(detail, h("div", { class: "col intro" },
      h("h2", { class: "h-detail", text: t("packs.emptyTitle") }),
      h("p", { class: "body", text: t("packs.emptyBody") }),
      importForm(doImport, true),
      hint(t("packs.emptyHint"))));
    return;
  }

  const form = importForm(doImport, false);
  form.hidden = true;
  const rows = h("ul", { class: "rows gap" });
  mount(list,
    h("div", { class: "listhead" },
      h("span", { class: "t-meta", text: t("packs.known", { n: packs.length }) }),
      h("span", { class: "grow" }),
      h("button", { class: "btn quiet sm", type: "button", "aria-expanded": "false", onclick: (e) => {
        form.hidden = !form.hidden;
        e.currentTarget.setAttribute("aria-expanded", String(!form.hidden));
        if (!form.hidden) form.querySelector("input").focus();
      } }, icon("plus", 16), t("packs.import"))),
    form, rows);

  let shown = 0; // only the latest selection mounts its detail
  const draw = () => {
    mount(rows, packs.map((p) => packRow(p, p.path === current, () => {
      current = p.path;
      selectPack(p.path);
      draw();
    })));
    const turn = ++shown;
    showPack(detail, packs.find((p) => p.path === current), { ...ctx, stale: () => ctx.stale() || turn !== shown }, doImport);
  };
  if (!packs.some((p) => p.path === current)) current = packs[0].path;
  draw();
}

function importForm(submit, primary) {
  const input = h("input", { class: "input mono", id: "pack-path", type: "text", name: "pack", placeholder: t("packs.placeholder"), autocomplete: "off", spellcheck: "false", "aria-describedby": "pack-help" });
  const err = h("div", { class: "err", role: "alert" });
  const go = h("button", { class: primary ? "btn primary" : "btn", type: "submit", text: t("packs.importBtn") });
  return h("form", { class: primary ? "form" : "form addform", onsubmit: async (e) => {
    e.preventDefault();
    const v = input.value.trim();
    mount(err);
    if (!isAbsolute(v)) {
      err.textContent = t("packs.notAbsolute");
      input.setAttribute("aria-invalid", "true");
      return;
    }
    input.removeAttribute("aria-invalid");
    go.disabled = true;
    await submit(v, err);
    go.disabled = false;
  } },
  h("div", { class: "field" },
    h("label", { class: "label", for: "pack-path", text: t("packs.pathLabel") }),
    h("div", { class: primary ? "inline wide" : "inline" }, input, go),
    h("p", { class: "help", id: "pack-help", text: t("packs.pathHelp") }), err));
}

function packRow(p, pressed, pick) {
  const on = isInstalled(p.path);
  return h("li", {}, h("button", { class: "rowbtn tall", type: "button", "aria-pressed": String(pressed), onclick: pick },
    h("span", { class: "lead-dot" }, h("span", { class: on ? "dot on" : "dot" })),
    h("span", { class: "main" },
      h("span", { class: "title" }, h("b", { text: p.name || t("packs.unreadable") }), p.version ? h("span", { class: "t-meta mono", text: p.version }) : null),
      h("span", { class: "t-path", text: tilde(p.path) })),
    h("span", { class: "side-mark" }, on ? h("span", { class: "pill sm you", text: t("packs.installed") })
      : p.error ? h("span", { class: "pill sm warn", text: t("packs.unreadableShort") }) : null)));
}

// once is an action button that its own request disables.
function once(button, run) {
  button.addEventListener("click", async () => {
    if (button.disabled) return;
    button.disabled = true;
    try {
      await run();
    } finally {
      button.disabled = false;
    }
  });
  return button;
}

async function showPack(detail, entry, ctx, doImport) {
  const path = entry.path;
  const out = h("div", { class: "out" });
  const doRemove = async () => {
    const res = await act("/api/packs/remove", { path });
    if (res.ok) setRegistry(res.data);
    if (ctx.stale()) return;
    if (!res.ok) {
      mount(out, failure(res.error));
      return;
    }
    ctx.again();
  };
  const removeBtn = (cls) => once(h("button", { class: cls, type: "button", text: t("packs.remove") }), doRemove);
  if (entry.error) {
    mount(detail, h("div", { class: "col intro" },
      h("p", { class: "t-path", text: tilde(path) }),
      h("h2", { class: "h-detail", text: t("packs.cannotRead") }),
      notice("bad", t("packs.unreadableBody"), entry.error),
      actions([removeBtn("btn")], t("packs.removeNote")), out));
    return;
  }
  mount(detail, busy(t("packs.reading")));
  const [res, outline] = await Promise.all([loadPack(path), loadOutline(path)]);
  if (ctx.stale()) return;
  const pack = res.data && res.data.pack;
  if (!pack) {
    mount(detail, h("div", { class: "col intro" },
      h("p", { class: "t-path", text: tilde(path) }),
      h("h2", { class: "h-detail", text: t("packs.cannotRead") }),
      failure(res.error), actions([removeBtn("btn")], t("packs.removeNote")), out));
    return;
  }
  const on = isInstalled(path);
  const s = installed();
  const enc = encodeURIComponent(path);
  const target = outline.ok ? outline.data.copyTarget : "";
  const copy = h("button", { class: "menu-item", type: "button", text: t("packs.copy"), onclick: (e) => {
    e.currentTarget.closest("details").open = false;
    copyForm(out, path, target, ctx, doImport);
  } });
  const more = h("details", { class: "menu" }, h("summary", { class: "btn", "aria-label": t("packs.more") }, t("packs.more")),
    h("div", { class: "menu-list" }, copy, removeBtn("menu-item")));

  const intro = h("div", { class: "col intro" },
    h("div", { class: "meta" },
      on ? h("span", { class: "pill you", text: t("packs.installedMode", { mode: lower(t("mode." + s.mode)) }) }) : h("span", { class: "pill state", text: t("packs.notInstalled") }),
      on ? h("span", { class: "t-meta", text: t("packs.installedSince", { variant: s.variant, date: fmtDate(s.installedAt) }) }) : null),
    h("h2", { class: "h-page" }, pack.name + " ", h("span", { class: "ver", text: pack.version })),
    h("p", { class: "caps" }, h("span", { class: "t-path", text: tilde(path) }),
      h("span", { class: "t-meta", text: t("packs.engine", { v: pack.engine }) }), h("span", { class: "t-meta", text: t("packs.rulesync", { v: pack.rulesync }) })),
    actions([
      h("a", { class: "btn primary", href: "#/plan/" + enc, text: t(on ? "packs.reinstall" : "packs.planInstall") }),
      h("a", { class: "btn", href: "#/tree/" + enc, text: t("packs.seeFiles") }),
      more,
    ], !on && s ? t("packs.replacesNote", { name: pack.name, other: s.pack.name }) : t("packs.planNote")),
    out);

  const warnings = res.ok ? res.data.warnings : res.data.diagnostics;
  const layers = outline.ok ? outline.data.layers : [];
  const of = (kind) => layers.filter((l) => l.kind === kind);
  const optional = pack.optionalHarnesses.length > 0;
  mount(detail, intro, h("div", { class: "col" },
    !res.ok ? failure(res.error) : null,
    sec(t("packs.gives"), "", h("ul", { class: "facts" }, highlights(pack, outline.ok ? outline.data : null).map((x) => h("li", { text: x })))),
    sec(t("packs.harnesses"), t(optional ? "packs.harnessesSome" : "packs.harnessesAll"), h("ul", { class: "fls" },
      [...pack.harnesses.map((id) => harnessRow(id, false)), ...pack.optionalHarnesses.map((id) => harnessRow(id, true))]),
    hint(t("hint.harness"))),
    !outline.ok ? failure(outline.error) : null,
    of("base").map((l) => sec(t("packs.base"), t("hint.base"), layerBody(l, [lm("base"), h("span", { class: "t-meta", text: t("packs.userScope") })]))),
    of("variant").length ? sec(t("packs.variants"), t("hint.variant"), h("div", { class: "layers" }, of("variant").map((l) => layerBody(l, [lm(l.name),
      l.name === pack.defaultVariant ? h("span", { class: "pill sm state", text: t("packs.default") }) : null,
      on && s.variant === l.name ? h("span", { class: "pill sm you", text: t("packs.installed") }) : null,
      pack.sharedSkills.includes(l.name) ? h("span", { class: "t-meta", text: t("packs.sharedSkills") }) : null])))) : null,
    of("profile").length ? sec(t("packs.profiles"), t("hint.profile"), h("div", { class: "layers" }, of("profile").map((l) => {
      const pr = pack.profiles.find((x) => x.name === l.name) || { description: "", hints: [] };
      return layerBody(l, [lm(l.name), pr.description ? h("span", { class: "authored" }, h("span", { class: "by", text: t("packs.fromPack") }), pr.description) : null,
        pr.hints.length ? h("span", { class: "t-meta full", text: t("packs.hints", { list: pr.hints.join(", ") }) }) : null]);
    }))) : null,
    outline.ok && outline.data.mods.length ? sec(t("packs.modsTitle"), t("hint.mod"), h("ul", { class: "fls" }, outline.data.mods.map((m) => h("li", { class: "fl" }, h("span", { class: "name mono", text: m }),
      h("span", { class: "t-meta", text: t("packs.namedBy", { list: pack.profiles.filter((p) => p.mods.includes(m)).map((p) => p.name).join(", ") || "—" }) }))))) : null,
    warnings && warnings.length ? sec(t(res.ok ? "packs.warnings" : "packs.problems"), "", h("ul", { class: "list" }, warnings.map((d) =>
      warning(d.code, d.file ? d.file + (d.line ? ":" + d.line : "") : "", d.message)))) : null));
}

// The types of a layer, in the order the pack format lists them.
const types = ["rules", "skills", "subagents", "commands", "mcpServers", "hooks", "permissions", "settings", "tools", "mods"];

function layerBody(l, head) {
  const c = l.contents;
  const pairs = types.map((k) => {
    const v = c[k];
    if (v === true) return [t("type." + k), t("packs.included")];
    if (!Array.isArray(v) || v.length === 0) return null;
    if (k === "settings") return [t("type." + k), v.map(harnessName).join(", ")];
    return [t("type." + k), h("span", { class: "chips" }, v.map((x) => h("span", { class: "chip", text: x })))];
  }).filter(Boolean);
  return h("div", { class: "layer" }, h("div", { class: "layer-head" }, head),
    pairs.length ? kv(pairs) : h("p", { class: "quietp", text: t("packs.emptyLayer") }),
    h("p", { class: "src-path", text: t("packs.inFolder", { path: l.path + "/" }) }));
}

function highlights(pack, outline) {
  const names = [...pack.harnesses, ...pack.optionalHarnesses].map(harnessName);
  const out = [t("packs.hl.source", { list: names.join(", ") })];
  if (pack.variants.length) out.push(t("packs.hl.variants", { n: pack.variants.length, list: pack.variants.join(", ") }));
  if (pack.profiles.length) out.push(t("packs.hl.profiles", { n: pack.profiles.length, list: pack.profiles.map((p) => p.name).join(", ") }));
  if (outline && outline.mods.length) out.push(t("packs.hl.mods", { n: outline.mods.length, list: outline.mods.join(", ") }));
  if (outline && outline.onboarding) out.push(t("packs.hl.onboarding"));
  out.push(t("packs.hl.isolated"));
  return out;
}

function harnessRow(id, optional) {
  return h("li", { class: "fl" }, h("span", { class: "name", text: harnessName(id) }), h("span", { class: "id", text: id }),
    optional ? h("span", { class: "pill sm state end", text: t("packs.optional") }) : null);
}

// copyForm asks where to copy the pack, by default the free <name>-copy
// next to it that the server suggests.
function copyForm(area, path, target, ctx, doImport) {
  const input = h("input", { class: "input mono", id: "copy-path", type: "text", value: target, autocomplete: "off", spellcheck: "false", "aria-describedby": "copy-help" });
  const out = h("div", { class: "out" });
  const go = h("button", { class: "btn primary", type: "submit", text: t("packs.copyGo") });
  mount(area, h("form", { class: "form well", onsubmit: async (e) => {
    e.preventDefault();
    const to = input.value.trim();
    if (!isAbsolute(to)) {
      mount(out, notice("bad", t("packs.notAbsolute")));
      return;
    }
    if (go.disabled) return;
    go.disabled = true;
    const res = await act("/api/packs/copy", { from: path, to });
    go.disabled = false;
    if (ctx.stale()) return;
    if (!res.ok) {
      mount(out, failure(res.error));
      return;
    }
    const err = h("div", { class: "err", role: "alert" });
    mount(area, h("div", { class: "stack" },
      notice("good", t("packs.copied", { n: res.data.files, path: tilde(res.data.to) })),
      h("p", { class: "body", text: t("packs.copyNext") }),
      actions([once(h("button", { class: "btn", type: "button", text: t("packs.importCopy") }), () => doImport(res.data.to, err))], t("packs.importCopyNote")),
      err));
  } },
  h("div", { class: "field" },
    h("label", { class: "label", for: "copy-path", text: t("packs.copyLabel") }),
    h("div", { class: "inline wide" }, input, go),
    h("p", { class: "help", id: "copy-help", text: t("packs.copyHelp") })), out));
  input.focus();
}
