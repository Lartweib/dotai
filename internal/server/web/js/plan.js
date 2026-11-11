// The Install plan board: dotai install --dry-run for the pack of the
// route, then the install of exactly that plan. The plan carries the
// digest of what it shows; the install sends it back, and the server
// refuses with plan-changed, and the new plan, when the plan is no longer
// the same (decision 0043). Whether the plan is blocked and whether it
// needs consent come with the plan.

import { actions, boardHead, busy, failure, h, hint, lm, mount, notice, op, sec, warning } from "./dom.js";
import { guard, rel } from "./flows.js";
import { t } from "./i18n.js";
import { act, harnessName, installed, isInstalled, loadPack, post, selectedPack, shared, tilde } from "./store.js";

export async function render(root, ctx) {
  const pack = ctx.arg || selectedPack();
  const pane = h("div", { class: "detail solo" });
  mount(root, boardHead("plan"), pane);
  if (!pack) {
    mount(pane, h("div", { class: "col intro" },
      h("h2", { class: "h-detail", text: t("plan.noPackTitle") }),
      h("p", { class: "body", text: t("plan.noPackBody") }),
      actions([h("a", { class: "btn primary", href: "#/packs", text: t("tree.goPacks") })])));
    return;
  }
  const s = installed();
  const state = { mode: isInstalled(pack) && s ? s.mode : "", variant: "", consent: false };
  const info = await loadPack(pack);
  if (ctx.stale()) return;
  const summary = info.data && info.data.pack;

  const replan = async (msg) => {
    mount(pane, busy(t("plan.planning")));
    const res = await post("/api/install/plan", { pack, mode: state.mode, variant: state.variant }, ctx.signal);
    if (ctx.stale()) return;
    show(res, msg);
  };

  const show = (res, msg) => {
    if (!res.data || !res.data.ops) {
      mount(pane, h("div", { class: "col wide intro" }, crumbs(pack, summary), failure(res.error),
        actions([h("button", { class: "btn", type: "button", onclick: () => replan(), text: t("plan.again") })])));
      return;
    }
    const p = res.data;
    state.mode = p.mode;
    state.variant = p.variant;
    const out = h("div", { class: "out" });
    const consent = h("input", { type: "checkbox" });
    consent.checked = state.consent;
    const go = h("button", { class: "btn primary", type: "button", text: t("plan.install") });
    const says = h("p", { class: "help" });
    // A plan with diagnostics comes without a digest: the page cannot act on it.
    const g = guard(go, () => !!p.digest && !p.blocked && (!p.consent || state.consent));
    const sync = () => {
      g.sync();
      says.textContent = p.blocked || !p.digest ? t("plan.refused")
        : p.consent && !state.consent ? t("plan.waitsConsent")
          : t("plan.willWrite", { roots: roots(p) });
    };
    consent.addEventListener("change", () => { state.consent = consent.checked; sync(); });
    sync();
    go.addEventListener("click", async () => {
      mount(out, busy(t("plan.installing")));
      const r = await g.run(() => act("/api/install", { pack, mode: p.mode, variant: p.variant, digest: p.digest, yes: state.consent }));
      if (!r || ctx.stale()) return;
      if (r.ok) {
        await ctx.refresh();
        if (ctx.stale()) return;
        mount(pane, done(pack, summary, r.data));
        return;
      }
      if (r.error.code === "plan-changed") {
        state.consent = false;
        show({ ok: true, data: r.data }, failure(r.error));
        return;
      }
      mount(out, failure({ ...r.error, message: r.error.message + running(r.data) }));
    });

    const variants = summary ? summary.variants : [];
    const backups = shared.status ? tilde(shared.status.backupsPath) : "~/.dotai/backups";
    mount(pane,
      h("div", { class: "col wide intro" },
        crumbs(pack, summary),
        h("h2", { class: "h-page", text: p.variant ? t("plan.headingVariant", { name: p.pack ? p.pack.name : "", variant: p.variant }) : t("plan.heading", { name: p.pack ? p.pack.name : "" }) }),
        h("p", { class: "meta" }, p.variant ? lm(p.variant) : null,
          h("span", { class: "t-meta", text: [replacedNote(p), managed(p).map((x) => harnessName(x.id)).join(", ")].filter(Boolean).join(" · ") })),
        msg || null),
      h("div", { class: "col wide form" },
        variants.length > 1 ? h("fieldset", { class: "field" }, h("legend", { class: "label", text: t("plan.variant") }), hint(t("hint.variant")),
          h("div", { class: "tabs", role: "radiogroup", "aria-label": t("plan.variant") }, variants.map((v) =>
            h("button", { class: "btn sm", type: "button", role: "radio", "aria-checked": String(v === p.variant), "aria-selected": String(v === p.variant), onclick: () => { state.variant = v; state.consent = false; replan(); }, text: v })))) : null,
        modes(p.mode, (m) => { state.mode = m; state.consent = false; replan(); })),
      h("div", { class: "col wide" },
        h("p", { class: "summary", text: counts(p) }),
        p.diagnostics.length ? sec(t("plan.problems"), "", h("ul", { class: "list" }, p.diagnostics.map((d) => warning(d.code, d.file || "", d.message)))) : null,
        sections(p),
        sec(t("plan.warnings"), "", warnings(p)),
        sec(t("plan.backup"), t("hint.backup"),
          h("div", { class: "well" }, h("p", { class: "t-path ink", text: backups + "/<time>_install" }),
            h("p", { class: "body", text: t(p.mode === "replace" ? "plan.backupReplace" : "plan.backupIsolated") })),
          h("p", { class: "help", text: t("plan.backupId") })),
        h("div", { class: "confirmbar" },
          p.consent ? h("label", { class: "check" }, consent, h("span", { text: t("plan.consent") })) : null,
          actions([go, h("a", { class: "btn quiet", href: "#/packs", text: t("common.cancel") })], ""),
          says, out)));
  };
  await replan();
}

function crumbs(pack, summary, installedNow) {
  return h("p", { class: "meta" }, h("a", { class: "t-meta", href: "#/packs", text: summary ? summary.name + " " + summary.version : tilde(pack) }),
    installedNow ? h("span", { class: "pill sm you", text: t("packs.installed") }) : h("span", { class: "pill sm state", text: t("plan.nothingYet") }));
}

function modes(current, pick) {
  return h("fieldset", { class: "field" }, h("legend", { class: "label", text: t("plan.mode") }),
    h("div", { class: "modes" }, ["isolated", "replace"].map((id) => {
      const input = h("input", { type: "radio", name: "mode", value: id, onchange: () => pick(id) });
      input.checked = id === current;
      return h("label", { class: "mode", "data-on": String(id === current) }, input,
        h("span", {}, h("b", { text: t("mode." + id) }), h("span", { text: t("mode." + id + "Desc") })));
    })));
}

const managed = (p) => p.harnesses.filter((x) => x.managed);
const roots = (p) => managed(p).map((x) => tilde(x.dir)).join(", ") || tilde(p.root);

function replacedNote(p) {
  const s = installed();
  if (!s || !p.pack) return "";
  if (s.pack.name === p.pack.name && s.variant !== p.variant) return t("plan.replaces", { name: s.variant });
  if (s.pack.name !== p.pack.name) return t("plan.replaces", { name: s.pack.name });
  return "";
}

function sections(p) {
  const groups = managed(p).map((x) => ({ id: x.id, name: harnessName(x.id), dir: x.dir }));
  if (p.ops.some((o) => !o.harness)) groups.push({ id: "", name: harnessName(""), dir: "" });
  return groups.map((g) => {
    const ops = p.ops.filter((o) => (o.harness || "") === g.id);
    const keep = ops.filter((o) => o.action === "unchanged").map((o) => rel(g.dir, o.target));
    const rows = [
      ...ops.filter((o) => o.action !== "unchanged").map((o) => op(o.action, rel(g.dir, o.target), "", keysOf(o))),
      ...p.wipes.filter((w) => w.harness === g.id).map((w) => op("wipe", rel(g.dir, w.path) + (w.kind === "dir" ? "/" : ""), "", w.collateral || t("plan.movesToBackup"))),
      ...p.wipeKeys.filter((k) => k.harness === g.id).map((k) => op("wipe", tilde(k.file) + " · " + k.key.join("."), "", t("plan.wipeKey"))),
      keep.length ? op("unchanged", keep.join(" · ")) : null,
    ];
    return sec(g.name, "", g.dir ? h("p", { class: "t-path", text: tilde(g.dir) }) : null,
      h("ul", { class: "list" }, rows.length ? rows : h("li", { class: "quietp", text: t("common.nothingToWrite") })));
  });
}

function keysOf(o) {
  if (!o.keys || o.keys.length === 0) return "";
  return t("plan.keys", { list: o.keys.map((k) => k.key + (k.action !== "create" ? " (" + t("action." + k.action) + ")" : "")).join(", ") });
}

function counts(p) {
  const n = (a) => p.ops.filter((o) => o.action === a).length;
  const parts = ["create", "replace", "remove"].filter((a) => n(a)).map((a) => t("plan.count." + a, { n: n(a) }));
  if (p.wipes.length) parts.push(t("plan.count.wipeEntries", { n: p.wipes.length }));
  if (p.wipeKeys.length) parts.push(t("plan.count.wipeKeys", { n: p.wipeKeys.length }));
  if (n("unchanged")) parts.push(t("plan.count.unchanged", { n: n("unchanged") }));
  return parts.join(" · ") || t("common.nothingToWrite");
}

// warnings says, per harness, one short line of what the plan warns about,
// with what dotai said behind it.
function warnings(p) {
  const relogin = p.harnesses.filter((x) => x.managed && x.relogin === "yes").map((x) => harnessName(x.id));
  const list = [
    ...p.refusals.map((r) => warning(r.code, t("note." + r.code, {}, ""), r.message)),
    relogin.length ? warning("relogin", t("plan.relogin", { n: relogin.length, list: relogin.join(", ") })) : null,
    ...p.harnesses.filter((x) => x.managed && x.leaks.length).map((x) => warning("still-shared", t("plan.stillShared", { name: harnessName(x.id) }), x.leaks.join("; ") + ".")),
    p.notices.length ? warning("notice", t("plan.notices", { n: p.notices.length }), p.notices.join("\n")) : null,
  ].filter(Boolean);
  if (p.consent) {
    list.unshift(warning("consent-required", t("plan.consentWarn", { entries: t("plan.entries", { n: p.wipes.length }), keys: t("plan.keyCount", { n: p.wipeKeys.length }) })));
  }
  return list.length ? h("ul", { class: "list" }, list) : h("p", { class: "quietp", text: t("plan.noWarnings") });
}

function running(data) {
  if (!data || !data.running || !data.running.length) return "";
  return " " + t("plan.running", { list: data.running.map((r) => r.name + " " + r.pid).join(", ") });
}

function done(pack, summary, res) {
  return h("div", { class: "col wide intro" },
    crumbs(pack, summary, true),
    h("h2", { class: "h-page", text: t("plan.doneTitle", { name: res.pack ? res.pack.name : "" }) }),
    notice("good", res.backup ? t("plan.doneBackup", { id: res.backup.id }) : t("plan.doneNothing")),
    res.warnings && res.warnings.length ? h("ul", { class: "list" }, warning("notice", t("plan.notices", { n: res.warnings.length }), res.warnings.join("\n"))) : null,
    actions([h("a", { class: "btn primary", href: "#/tree/" + encodeURIComponent(pack), text: t("packs.seeFiles") }),
      h("a", { class: "btn", href: "#/backups", text: t("nav.backups") }), h("a", { class: "btn quiet", href: "#/packs", text: t("nav.packs") })]));
}
