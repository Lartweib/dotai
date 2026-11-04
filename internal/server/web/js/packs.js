// The Packs board: the packs of the registry (~/.dotai/packs.json) on the
// left, the selected one on the right: what each of its layers holds, by
// type, as the server reads it, and what dotai validate says. Import adds a
// folder once it validates, copy makes a new folder to edit, and remove
// only forgets the entry.

import { actions, boardHead, busy, failure, h, hint, icon, kv, lm, mount, notice, sec } from "./dom.js";
import { fmtDate, t } from "./i18n.js";
import { act, harnessName, installed, isAbsolute, isInstalled, loadOutline, loadPack, registry, selectPack, selectedPack, setRegistry, tilde } from "./store.js";

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-label": t("packs.listLabel") });
  const detail = h("section", { class: "detail", "aria-label": t("packs.detailLabel") });
  mount(root, boardHead("packs"), list, detail);
  const packs = registry();
  ctx.setCount("packs", packs.length);
  let current = selectedPack();

  const form = importForm(async (path, err) => {
    const res = await act("/api/packs/import", { path });
    if (ctx.stale()) return;
    if (!res.ok) {
      mount(err, failure(res.error));
      return;
    }
    setRegistry(res.data);
    selectPack(path);
    ctx.again();
  });
  form.hidden = packs.length > 0;

  const rows = h("ul", { class: "rows gap" });
  mount(list,
    h("div", { class: "listhead" },
      h("span", { class: "t-meta", text: t("packs.known", { n: packs.length }) }),
      h("span", { class: "grow" }),
      packs.length ? h("button", { class: "btn quiet sm", type: "button", "aria-expanded": "false", onclick: (e) => {
        form.hidden = !form.hidden;
        e.currentTarget.setAttribute("aria-expanded", String(!form.hidden));
        if (!form.hidden) form.querySelector("input").focus();
      } }, icon("plus", 16), t("packs.import")) : null),
    form, rows);

  if (packs.length === 0) {
    mount(detail, h("div", { class: "col intro" },
      h("h2", { class: "h-detail", text: t("packs.emptyTitle") }),
      h("p", { class: "body", text: t("packs.emptyBody") }),
      hint(t("packs.emptyHint"))));
    return;
  }

  const draw = () => {
    mount(rows, packs.map((p) => packRow(p, p.path === current, () => {
      current = p.path;
      selectPack(p.path);
      draw();
    })));
    showPack(detail, packs.find((p) => p.path === current), ctx);
  };
  if (!packs.some((p) => p.path === current)) current = packs[0].path;
  draw();
}

function importForm(submit) {
  const input = h("input", { class: "input mono", id: "pack-path", type: "text", name: "pack", placeholder: t("packs.placeholder"), autocomplete: "off", spellcheck: "false", "aria-describedby": "pack-help" });
  const err = h("div", { class: "err", role: "alert" });
  const go = h("button", { class: "btn", type: "submit", text: t("packs.importBtn") });
  return h("form", { class: "form addform", onsubmit: async (e) => {
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
    h("div", { class: "inline" }, input, go),
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

async function showPack(detail, entry, ctx) {
  const path = entry.path;
  const forget = h("button", { class: "btn quiet", type: "button", text: t("packs.remove"), onclick: async () => {
    const res = await act("/api/packs/remove", { path });
    if (ctx.stale()) return;
    if (!res.ok) {
      mount(detail, failure(res.error));
      return;
    }
    setRegistry(res.data);
    ctx.again();
  } });
  if (entry.error) {
    mount(detail, h("div", { class: "col intro" },
      h("p", { class: "t-path", text: tilde(path) }),
      h("h2", { class: "h-detail", text: t("packs.cannotRead") }),
      notice("bad", entry.error),
      actions([forget], t("packs.removeNote"))));
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
      failure(res.error), actions([forget], t("packs.removeNote"))));
    return;
  }
  const on = isInstalled(path);
  const s = installed();
  const enc = encodeURIComponent(path);
  const copyArea = h("div", { class: "out" });
  const copyBtn = h("button", { class: "btn", type: "button", text: t("packs.copy"), onclick: () => copyForm(copyArea, path, pack.name, ctx) });

  const intro = h("div", { class: "col intro" },
    h("div", { class: "meta" },
      on ? h("span", { class: "pill you", text: t("packs.installedMode", { mode: t("mode." + s.mode).toLocaleLowerCase() }) }) : h("span", { class: "pill state", text: t("packs.notInstalled") }),
      on ? h("span", { class: "t-meta", text: t("packs.installedSince", { variant: s.variant, date: fmtDate(s.installedAt) }) }) : null),
    h("h2", { class: "h-page" }, pack.name + " ", h("span", { class: "ver", text: pack.version })),
    h("p", { class: "caps" }, h("span", { class: "t-path", text: tilde(path) }),
      h("span", { class: "t-meta", text: t("packs.engine", { v: pack.engine }) }), h("span", { class: "t-meta", text: t("packs.rulesync", { v: pack.rulesync }) })),
    actions([
      h("a", { class: "btn primary", href: "#/plan/" + enc, text: t(on ? "packs.planReinstall" : "packs.planInstall") }),
      h("a", { class: "btn", href: "#/tree/" + enc, text: t("packs.seeFiles") }),
      copyBtn, forget,
    ], !on && s ? t("packs.replacesNote", { name: pack.name, other: s.pack.name }) : t("packs.planNote")),
    copyArea);

  const warnings = res.ok ? res.data.warnings : res.data.diagnostics;
  const layers = outline.ok ? outline.data.layers : [];
  const of = (kind) => layers.filter((l) => l.kind === kind);
  mount(detail, intro, h("div", { class: "col" },
    !res.ok ? failure(res.error) : null,
    sec(t("packs.gives"), "", h("ul", { class: "facts" }, highlights(pack, outline.ok ? outline.data : null).map((x) => h("li", { text: x })))),
    sec(t("packs.harnesses"), t("hint.harness"), h("ul", { class: "fls" }, [...pack.harnesses.map((id) => harnessRow(id, "required")), ...pack.optionalHarnesses.map((id) => harnessRow(id, "optional"))])),
    !outline.ok ? failure(outline.error) : null,
    of("base").map((l) => sec(t("packs.base"), t("hint.base"), layerBody(l, [lm("base"), h("span", { class: "t-meta", text: t("packs.userScope") })]))),
    of("variant").length ? sec(t("packs.variants"), t("hint.variant"), h("div", { class: "layers" }, of("variant").map((l) => layerBody(l, [lm(l.name),
      l.name === pack.defaultVariant ? h("span", { class: "pill sm state", text: t("packs.default") }) : null,
      on && s.variant === l.name ? h("span", { class: "pill sm you", text: t("packs.installed") }) : null,
      pack.sharedSkills.includes(l.name) ? h("span", { class: "t-meta", text: t("packs.sharedSkills") }) : null])))) : null,
    of("profile").length ? sec(t("packs.profiles"), t("hint.profile"), h("div", { class: "layers" }, of("profile").map((l) => {
      const pr = pack.profiles.find((x) => x.name === l.name) || { description: "", hints: [] };
      return layerBody(l, [lm(l.name), h("span", { class: "text", text: pr.description }),
        pr.hints.length ? h("span", { class: "t-meta full", text: t("packs.hints", { list: pr.hints.join(", ") }) }) : null]);
    }))) : null,
    outline.ok && outline.data.mods.length ? sec(t("packs.modsTitle"), t("hint.mod"), h("ul", { class: "fls" }, outline.data.mods.map((m) => h("li", { class: "fl" }, h("span", { class: "name mono", text: m }),
      h("span", { class: "t-meta", text: t("packs.namedBy", { list: pack.profiles.filter((p) => p.mods.includes(m)).map((p) => p.name).join(", ") || "—" }) }))))) : null,
    warnings && warnings.length ? sec(t(res.ok ? "packs.warnings" : "packs.problems"), "", h("ul", { class: "list" }, warnings.map((d) => h("li", { class: "wn" }, icon("warn", 16),
      h("span", { class: "wt" }, h("span", { class: "code", text: d.code + (d.file ? " · " + d.file + (d.line ? ":" + d.line : "") : "") }), h("span", { class: "msg", text: d.message })))))) : null));
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
    h("p", { class: "t-path", text: l.path }));
}

function highlights(pack, outline) {
  const names = [...pack.harnesses, ...pack.optionalHarnesses].map(harnessName);
  const out = [t("packs.hl.source", { list: names.join(", ") })];
  if (pack.variants.length) out.push(t("packs.hl.variants", { n: pack.variants.length, list: pack.variants.join(", ") }));
  if (pack.profiles.length) out.push(t("packs.hl.profiles", { n: pack.profiles.length, list: pack.profiles.map((p) => p.name).join(", ") }));
  if (outline && outline.mods.length) out.push(t("packs.hl.mods", { n: outline.mods.length, list: outline.mods.join(", ") }));
  out.push(t("packs.hl.isolated"));
  return out;
}

function harnessRow(id, need) {
  return h("li", { class: "fl" }, h("span", { class: "name", text: harnessName(id) }), h("span", { class: "id", text: id }), h("span", { class: "pill sm state end", text: t("packs." + need) }));
}

// copyForm asks where to copy the pack: by default a new folder next to
// it, named <name>-copy, which the server refuses if it exists.
function copyForm(area, path, name, ctx) {
  const parent = path.replace(/[\\/][^\\/]*$/, "");
  const sep = path.includes("\\") ? "\\" : "/";
  const input = h("input", { class: "input mono", id: "copy-path", type: "text", value: parent + sep + name + "-copy", autocomplete: "off", spellcheck: "false", "aria-describedby": "copy-help" });
  const out = h("div", { class: "out" });
  const go = h("button", { class: "btn primary", type: "submit", text: t("packs.copyGo") });
  mount(area, h("form", { class: "form well", onsubmit: async (e) => {
    e.preventDefault();
    const to = input.value.trim();
    if (!isAbsolute(to)) {
      mount(out, notice("bad", t("packs.notAbsolute")));
      return;
    }
    go.disabled = true;
    const res = await act("/api/packs/copy", { from: path, to });
    go.disabled = false;
    if (ctx.stale()) return;
    if (!res.ok) {
      mount(out, failure(res.error));
      return;
    }
    mount(area, h("div", { class: "stack" },
      notice("good", t("packs.copied", { n: res.data.files, path: tilde(res.data.to) })),
      h("p", { class: "body", text: t("packs.copyNext") }),
      actions([h("button", { class: "btn", type: "button", text: t("packs.importCopy"), onclick: async () => {
        const r = await act("/api/packs/import", { path: res.data.to });
        if (ctx.stale()) return;
        if (!r.ok) {
          mount(area, failure(r.error));
          return;
        }
        setRegistry(r.data);
        selectPack(res.data.to);
        ctx.again();
      } })], t("packs.importCopyNote"))));
  } },
  h("div", { class: "field" },
    h("label", { class: "label", for: "copy-path", text: t("packs.copyLabel") }),
    h("div", { class: "inline wide" }, input, go),
    h("p", { class: "help", id: "copy-help", text: t("packs.copyHelp") })), out));
  input.focus();
}
