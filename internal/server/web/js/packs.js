// The Packs board: the packs of the registry (~/.dotai/packs.json) on the
// left, each by its identity (github.com/<owner>/<repo> or local/<name>),
// the selected one on the right: where it comes from, what each of its
// layers holds, by type, as the server reads it, and what dotai validate
// says. Adding a pack and updating a git pack are boards of their own
// (packadd.js, packupdate.js); copy makes a new folder to edit, and remove,
// once confirmed, forgets the entry and deletes a git pack's clone.

import { actions, boardHead, closeMenu, failure, h, hint, icon, kv, lm, loading, menu, mount, notice, sec, settle, submitGuard, tech, warning } from "./dom.js";
import { diagnostics, packWarnings, sourceLine } from "./packreview.js";
import { shortCommit, visible } from "./packsource.js";
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
      actions([h("a", { class: "btn primary", href: "#/add" }, icon("plus", 16), t("packs.add"))], t("packs.addNote")),
      hint(t("packs.emptyHint"))));
    return;
  }

  const rows = h("ul", { class: "rows gap" });
  mount(list,
    h("div", { class: "listhead" },
      h("span", { class: "t-meta", text: t("packs.known", { n: packs.length }) }),
      h("span", { class: "grow" }),
      h("a", { class: "btn quiet sm", href: "#/add" }, icon("plus", 16), t("packs.add"))),
    rows);

  let shown = 0; // only the latest selection mounts its detail
  const draw = () => {
    settle(list);
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

function packRow(p, pressed, pick) {
  const on = isInstalled(p.path);
  return h("li", {}, h("button", { class: "rowbtn tall", type: "button", "aria-pressed": String(pressed), onclick: pick },
    h("span", { class: "lead-dot" }, h("span", { class: on ? "dot on" : "dot" })),
    h("span", { class: "main" },
      h("span", { class: "title" }, h("b", { text: p.name ? visible(p.name) : t("packs.unreadable") }), p.version ? h("span", { class: "t-meta mono", text: p.version }) : null),
      sourceLine(p)),
    h("span", { class: "side-mark" }, on ? h("span", { class: "pill sm you", text: t("packs.installed") })
      : p.error ? h("span", { class: "pill sm warn", text: t("packs.unreadableShort") }) : null)));
}

// once is an action button that its own request disables.
function once(button, run) {
  button.addEventListener("click", () => submitGuard(button, run));
  return button;
}

async function showPack(detail, entry, ctx, doImport) {
  const path = entry.path;
  const out = h("div", { class: "out" });
  // A removal asks first, and says what it deletes: a git pack's clone
  // goes, a local pack's folder stays.
  const removeBtn = (cls) => h("button", { class: cls, type: "button", text: t("packs.removeAsk"), onclick: (e) => {
    const m = e.currentTarget.closest("details");
    if (m) closeMenu(m);
    mount(out, removeConfirm(entry, ctx, out));
    out.querySelector("button").focus();
  } });
  if (entry.error) {
    mount(detail, h("div", { class: "col intro" },
      sourceLine(entry),
      h("p", { class: "t-path", text: tilde(path) }),
      h("h2", { class: "h-detail", text: t("packs.cannotRead") }),
      notice("bad", t("packs.unreadableBody"), entry.error),
      actions([removeBtn("btn")]), out));
    return;
  }
  loading(detail, t("packs.reading"));
  const [res, outline] = await Promise.all([loadPack(path), loadOutline(path)]);
  if (ctx.stale()) return;
  const pack = res.data && res.data.pack;
  if (!pack) {
    mount(detail, h("div", { class: "col intro" },
      sourceLine(entry),
      h("h2", { class: "h-detail", text: t("packs.cannotRead") }),
      failure(res.error), actions([removeBtn("btn")]), out));
    return;
  }
  const on = isInstalled(path);
  const s = installed();
  const enc = encodeURIComponent(path);
  const target = outline.ok ? outline.data.copyTarget : "";
  const copy = h("button", { class: "menu-item", type: "button", text: t("packs.copy"), onclick: (e) => {
    closeMenu(e.currentTarget.closest("details"));
    copyForm(out, path, target, ctx, doImport);
  } });
  const more = menu(t("packs.more"), [copy, removeBtn("menu-item")]);

  const intro = h("div", { class: "col intro" },
    h("div", { class: "meta" },
      on ? h("span", { class: "pill you", text: t("packs.installedMode", { mode: lower(t("mode." + s.mode)) }) }) : h("span", { class: "pill state", text: t("packs.notInstalled") }),
      on ? h("span", { class: "t-meta", text: t("packs.installedSince", { variant: s.variant, date: fmtDate(s.installedAt) }) }) : null),
    h("h2", { class: "h-page" }, visible(pack.name) + " ", h("span", { class: "ver", text: visible(pack.version) })),
    h("p", { class: "caps" }, h("span", { class: "t-path", text: tilde(path) }),
      h("span", { class: "t-meta", text: t("packs.engine", { v: pack.engine }) }), h("span", { class: "t-meta", text: t("packs.rulesync", { v: pack.rulesync }) })),
    origin(entry),
    actions([
      h("a", { class: "btn primary", href: "#/plan/" + enc, text: t(on ? "packs.reinstall" : "packs.planInstall") }),
      h("a", { class: "btn", href: "#/tree/" + enc, text: t("packs.seeFiles") }),
      entry.source ? h("a", { class: "btn", href: "#/update/" + encodeURIComponent(entry.id), text: t("packs.update") }) : null,
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
    // The layers, from the base to the mods, on one lane.
    h("div", { class: "lanes" },
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
      outline.ok ? onboarding(outline.data.onboarding) : null,
      outline.ok && outline.data.mods.length ? sec(t("packs.modsTitle"), t("hint.mod"), h("ul", { class: "fls" }, outline.data.mods.map((m) => h("li", { class: "fl" }, h("span", { class: "name mono", text: m }),
        h("span", { class: "t-meta", text: t("packs.namedBy", { list: pack.profiles.filter((p) => p.mods.includes(m)).map((p) => p.name).join(", ") || "—" }) }))))) : null),
    warnings && warnings.length ? sec(t(res.ok ? "packs.warnings" : "packs.problems"), "", diagnostics(warnings)) : null));
}

// onboarding is how a repository gets the pack's profiles (decision 0045):
// dotai's profiling skill, which an install writes from the profiles, with
// the skill as it would be, or the pack's own item that replaces it.
function onboarding(o) {
  if (o.mode === "dotai") {
    return sec(t("packs.onboarding"), t("hint.onboarding"),
      h("p", { class: "body", text: t("packs.onboarding.dotai") }),
      h("details", { class: "fold" }, h("summary", { text: t("packs.onboarding.preview") }), h("pre", { class: "src", text: o.content })));
  }
  if (!o.name) return sec(t("packs.onboarding"), t("hint.onboarding"), h("ul", { class: "list" }, warning("onboarding-invalid", t("packs.onboarding.invalid", { path: o.path }))));
  return sec(t("packs.onboarding"), t("hint.onboarding"),
    h("ul", { class: "list" }, warning("onboarding-replaced", t("packs.onboarding.custom", { path: o.path }))),
    o.content ? h("details", { class: "fold" }, h("summary", { text: t("packs.inFolder", { path: o.path }) }), h("pre", { class: "src", text: o.content })) : null);
}

// origin says where the pack comes from: its identity, and for a git pack
// the repository, the ref and the commit it is at.
function origin(entry) {
  const src = entry.source;
  return h("dl", { class: "kv origin" },
    h("dt", { text: t("packs.identity") }), h("dd", { class: "mono", text: visible(entry.id || "—") }),
    src ? [
      h("dt", { text: t("packs.repository") }), h("dd", { class: "mono", text: visible(src.url) }),
      h("dt", { text: t("packs.ref") }), h("dd", { class: "mono", text: visible(src.fullRef || src.ref) }),
      h("dt", { text: t("packs.commit") }), h("dd", { class: "mono", title: src.commit, text: shortCommit(src.commit) }),
    ] : [h("dt", { text: t("packs.source") }), h("dd", { text: t("packs.localSource") })]);
}

// removeConfirm asks before a removal, saying what it deletes, then
// removes the entry by its identity, or by its path when it has none.
function removeConfirm(entry, ctx, out) {
  const name = entry.id || tilde(entry.path);
  const err = h("div", { class: "out" });
  const go = once(h("button", { class: "btn danger", type: "button", text: t("packs.remove") }), async () => {
    const res = await act("/api/packs/remove", entry.id ? { id: entry.id } : { path: entry.path });
    if (res.ok) setRegistry(res.data);
    if (ctx.stale()) return;
    if (!res.ok) {
      mount(err, failure(res.error));
      return;
    }
    await ctx.refresh();
    if (ctx.stale()) return;
    if (res.data.warnings && res.data.warnings.length) {
      // What the removal could not delete stays on screen until it is read.
      mount(out, notice("good", t("packs.removed", { id: name })), packWarnings(res.data.warnings),
        actions([h("button", { class: "btn", type: "button", text: t("common.showAgain"), onclick: () => ctx.again() })]));
      return;
    }
    ctx.again();
  });
  return h("div", { class: "well confirm", role: "group", "aria-label": t("packs.removeAsk") },
    h("p", { class: "label", text: t("packs.removeTitle", { id: name }) }),
    h("p", { class: "body", text: t(entry.source ? "packs.removeGit" : "packs.removeLocal", { path: tilde(entry.path) }) }),
    h("p", { class: "help", text: t("packs.removeInstalls") }),
    actions([go, h("button", { class: "btn quiet", type: "button", text: t("common.cancel"), onclick: () => mount(out) })]), err);
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
  if (outline) out.push(t(outline.onboarding.mode === "dotai" ? "packs.hl.onboardingDotai" : "packs.hl.onboardingCustom"));
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
