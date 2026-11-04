// The Install plan board: dotai install --dry-run for the pack of the
// route, then the install of exactly that plan. The plan carries the
// digest of what it shows; the install sends it back, and the server
// refuses with plan-changed, and the new plan, when the plan is no longer
// the same (decision 0043). Whether the plan is blocked and whether it
// needs consent come with the plan.

import { busy, h, lm, mount, notice, op, plural, sec, warning } from "./dom.js";
import { guard, rel, word } from "./flows.js";
import { act, harnessName, installed, isInstalled, loadPack, post, selectedPack, shared, tilde } from "./store.js";

export async function render(root, ctx) {
  const pack = ctx.arg || selectedPack();
  root.className = "view single";
  if (!pack) {
    mount(root, h("div", { class: "col" }, notice("", "", "Add a pack on the Packs board first."), h("p", {}, h("a", { href: "#/packs", text: "Packs" }))));
    return;
  }
  const s = installed();
  const state = { mode: isInstalled(pack) && s ? s.mode : "", variant: "", consent: false };
  const info = await loadPack(pack);
  if (ctx.stale()) return;
  const summary = info.data && info.data.pack;

  const replan = async (msg) => {
    mount(root, busy("Planning the install: dotai stages the pack and runs rulesync, nothing is written..."));
    const res = await post("/api/install/plan", { pack, mode: state.mode, variant: state.variant }, ctx.signal);
    if (ctx.stale()) return;
    show(res, msg);
  };

  const show = (res, msg) => {
    if (!res.data || !res.data.ops) {
      mount(root, h("div", { class: "col wide intro" }, crumbs(pack, summary), notice("bad", res.error.code, res.error.message),
        h("div", { class: "row" }, h("button", { class: "btn", type: "button", onclick: () => replan(), text: "Plan again" }))));
      return;
    }
    const p = res.data;
    state.mode = p.mode;
    state.variant = p.variant;
    const out = h("div");
    const consent = h("input", { type: "checkbox" });
    consent.checked = state.consent;
    const go = h("button", { class: "btn primary", type: "button", text: "Install" });
    const says = h("p", { class: "t-cap" });
    // A plan with diagnostics comes without a digest: the page cannot act on it.
    const g = guard(go, () => !!p.digest && !p.blocked && (!p.consent || state.consent));
    const sync = () => {
      g.sync();
      says.textContent = p.blocked || !p.digest ? "The plan has problems or refusals: install refuses it."
        : p.consent && !state.consent ? "Install waits for your consent above."
          : "Install takes the backup, then writes into " + roots(p) + ".";
    };
    consent.addEventListener("change", () => { state.consent = consent.checked; sync(); });
    sync();
    go.addEventListener("click", async () => {
      mount(out, busy("Installing..."));
      const r = await g.run(() => act("/api/install", { pack, mode: p.mode, variant: p.variant, digest: p.digest, yes: state.consent }));
      if (!r || ctx.stale()) return;
      if (r.ok) {
        await ctx.refresh();
        if (ctx.stale()) return;
        mount(root, done(pack, summary, r.data));
        return;
      }
      if (r.error.code === "plan-changed") {
        state.consent = false;
        show({ ok: true, data: r.data }, notice("bad", r.error.code, r.error.message));
        return;
      }
      mount(out, notice("bad", r.error.code, r.error.message + running(r.data)));
    });

    const variants = summary ? summary.variants : [];
    const backups = shared.status ? tilde(shared.status.backupsPath) : "the backups directory";
    mount(root,
      h("div", { class: "col wide intro" },
        crumbs(pack, summary, "install plan, nothing written yet"),
        h("h1", { class: "h-page", text: "Install " + (p.pack ? p.pack.name : "the pack") + (p.variant ? " with the " + p.variant + " variant" : "") }),
        h("p", { class: "row" }, p.variant ? lm(p.variant) : null,
          h("span", { class: "t-cap", text: [replacedNote(p), managed(p).map((x) => harnessName(x.id)).join(", "), p.pack ? "rulesync " + p.pack.rulesync : ""].filter(Boolean).join(" · ") })),
        variants.length > 1 ? h("div", { class: "tabs", role: "tablist", "aria-label": "Variant" }, variants.map((v) =>
          h("button", { class: "btn sm", type: "button", role: "tab", "aria-selected": String(v === p.variant), onclick: () => { state.variant = v; state.consent = false; replan(); }, text: v }))) : null,
        modes(p.mode, (m) => { state.mode = m; state.consent = false; replan(); }),
        h("p", { class: "t-cap", text: counts(p) }),
        msg || null),
      h("div", { class: "col wide" },
        p.diagnostics.length ? sec("Problems", h("ul", {}, p.diagnostics.map((d) => warning(d.code, (d.file ? d.file + ": " : "") + d.message)))) : null,
        sections(p),
        sec("Warnings", warnings(p)),
        sec("Backup", h("div", { class: "sc stack" },
          h("div", { class: "well" }, h("p", { class: "mono t-path", text: backups + "/<time>_install" }),
            h("p", { class: "body", text: p.mode === "replace" ? "Wiped entries move into the backup as they are, links and timestamps kept. Overwritten files and the keys it deletes are copied first. The first replace install pins its backup: it holds your configuration from before dotai."
              : "Copies of the files it overwrites and removes, and the install state from before." })),
          h("p", { class: "t-cap", text: "The id is the UTC time of the install with milliseconds, set when it starts. Restore on the Backups board undoes it." }))),
        h("div", { class: "confirmbar" },
          p.consent ? h("label", { class: "check" }, consent, h("span", { text: "I read the plan. Move what dotai does not own into the backup." })) : null,
          h("div", { class: "row" }, go, h("a", { class: "btn quiet", href: "#/packs", text: "Cancel" })),
          says, out)));
  };
  await replan();
}

function crumbs(pack, summary, what) {
  return h("div", { class: "crumbs" }, h("a", { href: "#/packs", text: summary ? summary.name + " " + summary.version : tilde(pack) }),
    what ? [h("span", { class: "t-cap", "aria-hidden": "true", text: "/" }), h("span", { class: "t-cap", text: what })] : null);
}

function modes(current, pick) {
  const opts = [
    ["isolated", "Isolated", "Installs into ~/.dotai/isolated. Your own Claude Code, agy and OpenCode configuration stays as it is; dotai run starts a harness against this install."],
    ["replace", "Replace", "Installs into each harness's own configuration, as a fresh install. What dotai does not own moves into the backup."],
  ];
  return h("fieldset", {}, h("legend", { class: "t-label", text: "Mode" }), h("div", { class: "row stretch" }, opts.map(([id, label, text]) => {
    const input = h("input", { type: "radio", name: "mode", value: id, onchange: () => pick(id) });
    input.checked = id === current;
    return h("label", { class: "mode", "data-on": String(id === current) }, input, h("span", {}, h("b", { text: label }), h("span", { text })));
  })));
}

const managed = (p) => p.harnesses.filter((x) => x.managed);
const roots = (p) => managed(p).map((x) => tilde(x.dir)).join(", ") || tilde(p.root);

function replacedNote(p) {
  const s = installed();
  if (!s || !p.pack) return "";
  if (s.pack.name === p.pack.name && s.variant !== p.variant) return "replaces " + s.variant;
  if (s.pack.name !== p.pack.name) return "replaces " + s.pack.name;
  return "";
}

function sections(p) {
  const groups = managed(p).map((x) => ({ id: x.id, name: harnessName(x.id), dir: x.dir }));
  if (p.ops.some((o) => !o.harness)) groups.push({ id: "", name: harnessName(""), dir: "" });
  return groups.map((g) => {
    const ops = p.ops.filter((o) => (o.harness || "") === g.id);
    const keep = ops.filter((o) => o.action === "unchanged").map((o) => rel(g.dir, o.target));
    const rows = [
      ...ops.filter((o) => o.action !== "unchanged").map((o) => op(word(o.action), rel(g.dir, o.target), "", keysOf(o))),
      ...p.wipes.filter((w) => w.harness === g.id).map((w) => op(word("wipe"), rel(g.dir, w.path) + (w.kind === "dir" ? "/" : ""), "", w.collateral || "Moves into the backup.")),
      ...p.wipeKeys.filter((k) => k.harness === g.id).map((k) => op(word("wipe"), tilde(k.file) + " · " + k.key.join("."), "", "A key of a file dotai splits by key.")),
      keep.length ? op(word("unchanged"), keep.join(" · ")) : null,
    ];
    return h("section", { class: "sec", "aria-label": g.name }, h("h2", { text: g.name }),
      h("div", { class: "sc" }, g.dir ? h("p", { class: "t-path", text: tilde(g.dir) }) : null, h("ul", {}, rows.length ? rows : h("li", { class: "quietp", text: "Nothing to write." }))));
  });
}

function keysOf(o) {
  if (!o.keys || o.keys.length === 0) return "";
  return "Keys " + o.keys.map((k) => k.key + (k.action !== "create" ? " (" + word(k.action) + ")" : "")).join(", ") + ".";
}

function counts(p) {
  const n = (a) => p.ops.filter((o) => o.action === a).length;
  const parts = ["create", "replace", "remove"].filter((a) => n(a)).map((a) => n(a) + " to " + word(a));
  if (p.wipes.length || p.wipeKeys.length) parts.push(plural(p.wipes.length, "entry", "entries") + " and " + plural(p.wipeKeys.length, "key") + " to wipe");
  if (n("unchanged")) parts.push(n("unchanged") + " to keep");
  return parts.join(" · ") || "Nothing to write.";
}

function warnings(p) {
  const list = [
    ...p.refusals.map((r) => warning(r.code, r.message)),
    ...p.harnesses.filter((x) => x.managed && x.relogin === "yes").map((x) => warning("relogin", harnessName(x.id) + " asks you to log in again after this install.")),
    ...p.harnesses.filter((x) => x.managed && x.leaks.length).map((x) => warning("still-shared", harnessName(x.id) + ": " + x.leaks.join("; ") + ".")),
    ...p.notices.map((n) => warning("notice", n)),
  ];
  if (p.consent) {
    list.unshift(warning("consent-required", "The install moves into its backup what dotai does not own: " + plural(p.wipes.length, "entry", "entries") + " it wipes and " + plural(p.wipeKeys.length, "key") + " it deletes, and the files it overwrites."));
  }
  return list.length ? h("ul", {}, list) : h("p", { class: "quietp", text: "No warnings. dotai run starts a harness against the isolated install." });
}

function running(data) {
  if (!data || !data.running || !data.running.length) return "";
  return " Running: " + data.running.map((r) => r.name + " " + r.pid).join(", ") + ".";
}

function done(pack, summary, res) {
  return h("div", { class: "col wide intro" },
    crumbs(pack, summary, "installed"),
    h("h1", { class: "h-page", text: "Installed " + (res.pack ? res.pack.name : "the pack") }),
    notice("good", "installed", res.backup ? "The backup " + res.backup.id + " holds what was there before. Restore on the Backups board undoes this install." : "The install wrote no file, so it took no backup."),
    res.warnings && res.warnings.length ? h("ul", {}, res.warnings.map((w) => warning("warning", w))) : null,
    h("div", { class: "row" }, h("a", { class: "btn primary", href: "#/tree/" + encodeURIComponent(pack), text: "Merged tree" }), h("a", { class: "btn", href: "#/backups", text: "Backups" }), h("a", { class: "btn quiet", href: "#/packs", text: "Packs" })));
}
