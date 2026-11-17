// What the Add and Update boards share (decision 0044): the review of an
// update plan, bound to its digest, and the failures of a fetch in words.
// Everything a pack or git wrote (names, paths, diffs, refs, messages) is
// set as text, never as markup; a name or a path is shown with its control
// and invisible characters escaped, and an entry that is binary or unsafe
// shows only its hashes.

import { actions, busy, failure, h, icon, mount, notice, warning } from "./dom.js";
import { t } from "./i18n.js";
import { diffLines, failureKind, modeChanged, planNotes, shortCommit, shortDigest, tally, visible } from "./packsource.js";
import { tilde } from "./store.js";

// backLink leads back to the Packs board.
export function backLink() {
  return h("a", { class: "back", href: "#/packs" }, icon("back", 16), t("nav.packs"));
}

// sourceLine is the identity of a registry entry, then its ref and commit
// for a git pack.
export function sourceLine(entry) {
  const s = entry.source;
  return h("span", { class: "srcline" },
    h("span", { class: "mono id", text: visible(entry.id || tilde(entry.path)) }),
    s ? h("span", { class: "mono ref", text: visible(s.ref || s.fullRef) + " · " + shortCommit(s.commit) }) : null);
}

// diagnostics lists what validate reported, each at its file and line.
export function diagnostics(list) {
  return h("ul", { class: "list" }, list.map((d) =>
    warning(d.code, visible(d.file ? d.file + (d.line ? ":" + d.line : "") : ""), d.message)));
}

// fetchFailure shows the failure of an add or an update, sorted by
// failureKind: a fetch stopped by the time limit, or a request whose
// answer was lost, points to the command line; an invalid pack lists its
// problems and a refused tree what it refused.
export function fetchFailure(res, command) {
  const e = res.error || {};
  const data = res.data || {};
  const cmd = command ? h("p", { class: "src", text: command }) : null;
  switch (failureKind(res)) {
    case "timeout":
      return h("div", { class: "stack" }, notice("bad", t("packs.timedOut"), e.message), cmd);
    case "lost":
      return h("div", { class: "stack" }, notice("bad", t("packs.lost"), e.message ? e.code + ": " + e.message : ""),
        cmd ? h("p", { class: "help", text: t("packs.lostCommand") }) : null, cmd);
    case "busy":
      return notice("bad", t("packs.busy"), e.message);
    case "invalid":
      return h("div", { class: "stack" }, failure(e), diagnostics(data.diagnostics));
    case "tree":
      return h("div", { class: "stack" }, failure(e), h("ul", { class: "list" }, data.problems.map((p) =>
        warning("", visible(p.path || t("packs.wholeTree")), p.problem))));
    default:
      return failure(e);
  }
}

// packWarnings lists the warnings of an add, an update or its plan: the
// title from the code, the words of the pack or git as their detail.
export function packWarnings(list) {
  if (!list || list.length === 0) return null;
  return h("ul", { class: "list" }, list.map((w) => warning(w.code, t("packnote." + w.code, {}, ""), w.message)));
}

// review shows the update plan p in el and carries out exactly that plan.
// o.confirm(digest) sends it; on success o.apply(res) keeps what changed
// in the shared state, even when the viewer left the board, and o.done(res)
// shows it. o.busyText says what the request waits for, o.command is the
// command line to suggest and o.again starts over. A plan that changed
// before anything was written is shown again, new; any failure unticks
// the box, so that the user reads and confirms again.
export function review(el, p, o, msg) {
  const out = h("div", { class: "out" });
  const box = h("input", { type: "checkbox", id: "review-read" });
  const go = h("button", { class: "btn primary", type: "button", text: t("review.go", { commit: shortCommit(p.to.commit) }) });
  // idle, running, or done: a done plan keeps its button off until the
  // board is drawn again.
  let state = "idle";
  const sync = () => { go.disabled = state !== "idle" || !box.checked || !p.digest; };
  box.addEventListener("change", sync);
  sync();
  go.addEventListener("click", async () => {
    if (go.disabled) return;
    state = "running";
    sync();
    mount(out, working(o.busyText));
    let res;
    try {
      res = await o.confirm(p.digest);
      if (res.ok) {
        state = "done";
        await o.apply(res);
        if (o.ctx.stale()) return;
        o.done(res);
        return;
      }
    } catch (err) {
      if (state === "done") {
        if (!o.ctx.stale()) mount(out, notice("bad", t("review.shownFailed"), String(err && err.message)));
        return;
      }
      res = { ok: false, error: { code: "internal", message: String(err && err.message) } };
    }
    state = "idle";
    box.checked = false;
    sync();
    if (o.ctx.stale()) return;
    if (res.error.code === "plan-changed" && res.data && res.data.changes) {
      review(el, res.data, o, failure(res.error));
      return;
    }
    mount(out, fetchFailure(res, o.command),
      actions([h("button", { class: "btn", type: "button", text: t("common.tryAgain"), onclick: o.again })], t("review.tickAgain")));
  });

  const n = tally(p.changes);
  mount(el,
    msg ? h("div", { class: "stack" }, msg) : null,
    versions(p),
    p.missing ? notice("", t("review.missing")) : null,
    h("section", { class: "sheet panel", "aria-labelledby": "changes-h" },
      h("div", { class: "panel-head" },
        h("div", { class: "stack tight" }, h("p", { class: "kicker", text: t("review.runs") }),
          h("h2", { class: "h-sub big", id: "changes-h", text: t("review.changes", { n: p.changes.length }) })),
        p.changes.length ? h("span", { class: "t-path", text: counts(n) }) : null),
      h("p", { class: "help", text: t("review.changesHelp") }),
      p.changes.length ? h("div", { class: "changes" }, p.changes.map(change)) : h("p", { class: "quietp", text: t("review.noChanges") })),
    planNotes(p).length ? h("section", { class: "sheet panel" },
      h("p", { class: "kicker", text: t("review.notes") }), packWarnings(planNotes(p))) : null,
    h("div", { class: "confirmbar" },
      h("label", { class: "check", for: "review-read" }, box,
        h("span", { class: "check-text" }, h("b", { text: t("review.read") }), h("span", { class: "help block", text: t("review.readHelp") }))),
      actions([go, h("a", { class: "btn quiet", href: "#/packs", text: t("review.keep", { commit: shortCommit(p.from.commit) || t("review.current") }) })],
        t("review.bound", { digest: shortDigest(p.digest) })),
      out));
}

function counts(n) {
  return ["added", "modified", "removed", "modes", "binary"].filter((k) => n[k]).map((k) => t("review.count." + k, { n: n[k] })).join(" · ");
}

function versions(p) {
  const side = (label, s, cls) => h("div", { class: "ver " + cls },
    h("p", { class: "kicker", text: label }),
    h("b", { class: "mono", text: shortCommit(s.commit) || "—" }),
    h("span", { class: "t-path", text: visible(s.fullRef || s.ref) }),
    s.url ? h("span", { class: "t-path", text: visible(s.url) }) : null);
  return h("section", { class: "sheet versions", "aria-label": t("review.versions") },
    side(t("review.now"), p.from, "from"), h("div", { class: "bridge", "aria-hidden": "true" }), side(t("review.new"), p.to, "to"));
}

// The word and the sign of a change, as a plan tags what it does to a path.
const kinds = { added: "create", modified: "replace", removed: "remove" };

function change(c) {
  const head = h("div", { class: "chead" },
    h("span", { class: "mono cpath", text: visible(c.path) }),
    h("span", { class: "cmarks" },
      modeChanged(c) ? h("span", { class: "pill sm warn", text: t("review.mode", { from: c.modeBefore, to: c.modeAfter }) }) : null,
      h("span", { class: "tag", "data-a": kinds[c.change] || "replace", text: t("review.kind." + c.change, {}, c.change) })));
  if (c.binary) {
    return h("div", { class: "change" }, head, h("div", { class: "hidden-body" },
      h("p", { class: "body", text: t("review.notShown") }),
      h("dl", { class: "kv" },
        c.before ? [h("dt", { text: t("review.before") }), h("dd", { class: "mono", text: c.before })] : null,
        c.after ? [h("dt", { text: t("review.after") }), h("dd", { class: "mono", text: c.after })] : null)));
  }
  const lines = diffLines(c.diff);
  return h("div", { class: "change" }, head,
    lines.length ? h("pre", { class: "code", tabindex: "0", "aria-label": t("review.diffOf", { path: visible(c.path) }) }, lines.map((l) =>
      h("span", { class: "ln " + l.kind }, h("span", { class: "sg", "aria-hidden": "true", text: l.sign }), l.text)))
      : h("p", { class: "quietp pad", text: t(modeChanged(c) ? "review.modeOnly" : "review.noDiff") }));
}

// working is the busy line of a fetch, with what dotai does meanwhile.
export function working(text) {
  return h("div", { class: "stack" }, busy(text),
    h("ul", { class: "facts" }, ["fetch", "tree", "validate"].map((k) => h("li", { text: t("packs.work." + k) }))));
}
