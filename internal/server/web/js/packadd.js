// The Add a pack board (decision 0044): a repository on github.com, at the
// default branch, a tag or branch, or a full commit, or a folder on this
// machine for a pack being written. The server fetches one commit into a
// temporary folder, checks its tree and validates it before it lists the
// pack; nothing from the repository runs. A pack already added is an
// update: its plan is reviewed and confirmed by its digest, as on the
// Update board.

import { actions, boardHead, h, mount, notice, submitGuard } from "./dom.js";
import { t } from "./i18n.js";
import { backLink, fetchFailure, packWarnings, review, sourceLine, working } from "./packreview.js";
import { addCommand, gitIdentity, refProblem, shortCommit, visible } from "./packsource.js";
import { act, isAbsolute, loadPack, selectPack, setRegistry } from "./store.js";

export async function render(root, ctx) {
  const status = h("div", { class: "flow-out" });
  const sheet = h("section", { class: "sheet panel", "aria-label": t("add.sourceLabel") });
  const state = { mode: "github", kind: "default" };
  const tabs = h("div", { class: "tabs source", role: "group", "aria-label": t("add.whereLabel") });
  const drawTabs = () => mount(tabs, ["github", "folder"].map((m) => h("button", {
    type: "button", "aria-pressed": String(m === state.mode), onclick: () => {
      if (state.mode === m) return;
      state.mode = m;
      drawTabs();
      drawForm();
      mount(status);
    },
  }, h("b", { text: t("add.tab." + m) }), h("span", { text: t("add.tab." + m + "Help") }))));

  // hold dims the source tabs and the form, the version choice included,
  // while a request runs, so that no click or key acts on them.
  const hold = (on) => {
    for (const el of [tabs, sheet]) {
      el.inert = on;
      if (on) el.setAttribute("aria-busy", "true");
      else el.removeAttribute("aria-busy");
    }
  };

  // keep stores what an add answered, even when the viewer left the board;
  // show draws it, only on the board that asked.
  const keep = async (data) => {
    setRegistry(data);
    selectPack(data.pack.path);
    await ctx.refresh();
  };
  const show = async (data) => {
    const info = await loadPack(data.pack.path, true);
    if (ctx.stale()) return;
    mount(status, summary(data, info.data && info.data.pack, ctx));
  };
  const shownFailed = (err) => {
    if (!ctx.stale()) mount(status, notice("bad", t("add.shownFailed"), String(err && err.message)));
  };

  const run = async (request, command, again) => {
    hold(true);
    mount(status, working(t(state.mode === "github" ? "add.fetching" : "add.reading")));
    let res;
    try {
      res = await request();
    } finally {
      hold(false);
    }
    if (res.ok) {
      try {
        await keep(res.data);
        if (!ctx.stale()) await show(res.data);
      } catch (err) {
        shownFailed(err);
      }
      return;
    }
    if (ctx.stale()) return;
    if (res.error.code === "consent-required" && res.data && res.data.changes) {
      review(status, res.data, {
        ctx, command, again, busyText: t("review.updating"),
        confirm: (digest) => request(digest),
        apply: (r) => keep(r.data),
        done: (r) => show(r.data).catch(shownFailed),
      }, notice("", t("add.already", { id: visible(res.data.id) })));
      return;
    }
    mount(status, fetchFailure(res, command));
  };

  const addGit = (url, ref) => {
    const request = (digest) => act("/api/packs/add", digest ? { url, ref, digest } : { url, ref });
    const again = () => run(request, addCommand(url, ref), again);
    return again();
  };
  const addFolder = (path) => {
    const again = () => run(() => act("/api/packs/import", { path }), "", again);
    return again();
  };

  const drawForm = () => {
    mount(sheet, state.mode === "github" ? githubForm(state, addGit) : folderForm(addFolder));
    sheet.querySelector("input").focus();
  };

  mount(root, backLink(), boardHead("add"), h("div", { class: "flow" }, tabs, sheet, status));
  drawTabs();
  drawForm();
}

function githubForm(state, submit) {
  const url = h("input", { class: "input mono", id: "add-url", type: "text", placeholder: "https://github.com/owner/repo", autocomplete: "off", spellcheck: "false", "aria-describedby": "add-url-help" });
  const ref = h("input", { class: "input mono", id: "add-ref", type: "text", autocomplete: "off", spellcheck: "false", "aria-label": t("add.refLabel"), "aria-describedby": "add-ref-help" });
  const refHelp = h("p", { class: "help", id: "add-ref-help" });
  const id = h("span", { class: "mono", text: "—" });
  const idHint = h("p", { class: "help", hidden: true, text: t("add.notGithub") });
  const err = h("div", { class: "err", role: "alert" });
  const go = h("button", { class: "btn primary", type: "submit", text: t("add.go") });
  const kinds = h("div", { class: "tabs", role: "radiogroup", "aria-label": t("add.refLabel") });

  const sync = () => {
    mount(kinds, ["default", "named", "commit"].map((k) => h("button", {
      class: "btn sm", type: "button", role: "radio", "aria-checked": String(k === state.kind),
      text: t("add.kind." + k), onclick: () => { state.kind = k; sync(); if (k !== "default") ref.focus(); },
    })));
    ref.hidden = state.kind === "default";
    ref.placeholder = t("add.kind." + state.kind + "Placeholder");
    refHelp.textContent = t("add.kind." + state.kind + "Help");
  };
  const preview = () => {
    const v = url.value.trim();
    const found = gitIdentity(v);
    id.textContent = found || "—";
    idHint.hidden = !v || !!found;
  };
  url.addEventListener("input", preview);
  sync();

  return h("form", { class: "form", onsubmit: (e) => {
    e.preventDefault();
    mount(err);
    url.removeAttribute("aria-invalid");
    ref.removeAttribute("aria-invalid");
    const u = url.value.trim();
    if (!u) {
      err.textContent = t("add.urlEmpty");
      url.setAttribute("aria-invalid", "true");
      url.focus();
      return;
    }
    const problem = refProblem(state.kind, ref.value);
    if (problem) {
      err.textContent = t("add.ref." + problem);
      ref.setAttribute("aria-invalid", "true");
      ref.focus();
      return;
    }
    submitGuard(go, () => submit(u, state.kind === "default" ? "" : ref.value.trim()));
  } },
  h("div", { class: "field" },
    h("label", { class: "label", for: "add-url", text: t("add.urlLabel") }), url,
    h("p", { class: "help", id: "add-url-help", text: t("add.urlHelp") })),
  h("fieldset", { class: "field" }, h("legend", { class: "label", text: t("add.refLabel") }), kinds, ref, refHelp),
  h("div", { class: "idline" }, h("span", { class: "kicker", text: t("add.identity") }), id),
  idHint,
  h("hr", { class: "hr" }),
  h("div", { class: "formfoot" }, h("p", { class: "help", text: t("add.githubFoot") }), go),
  err);
}

function folderForm(submit) {
  const input = h("input", { class: "input mono", id: "add-dir", type: "text", placeholder: t("packs.placeholder"), autocomplete: "off", spellcheck: "false", "aria-describedby": "add-dir-help" });
  const err = h("div", { class: "err", role: "alert" });
  const go = h("button", { class: "btn primary", type: "submit", text: t("add.goFolder") });
  return h("form", { class: "form", onsubmit: (e) => {
    e.preventDefault();
    mount(err);
    const v = input.value.trim();
    if (!isAbsolute(v)) {
      err.textContent = t("packs.notAbsolute");
      input.setAttribute("aria-invalid", "true");
      input.focus();
      return;
    }
    input.removeAttribute("aria-invalid");
    submitGuard(go, () => submit(v));
  } },
  h("div", { class: "field" },
    h("label", { class: "label", for: "add-dir", text: t("packs.pathLabel") }), input,
    h("p", { class: "help", id: "add-dir-help", text: t("add.dirHelp") })),
  h("div", { class: "idline" }, h("span", { class: "kicker", text: t("add.identity") }), h("span", { class: "mono", text: t("add.localIdentity") })),
  h("hr", { class: "hr" }),
  h("div", { class: "formfoot" }, h("p", { class: "help", text: t("add.folderFoot") }), go),
  err);
}

// summary is what the add gave: the pack, where it comes from, what it
// holds and what to do next.
function summary(data, pack, ctx) {
  const e = data.pack;
  const enc = encodeURIComponent(e.path);
  const n = pack ? [
    ["harnesses", pack.harnesses.length + pack.optionalHarnesses.length],
    ["variants", pack.variants.length],
    ["profiles", pack.profiles.length],
  ] : [];
  return h("section", { class: "sheet panel", "aria-labelledby": "added-h" },
    h("div", { class: "panel-head" },
      h("div", { class: "stack tight" },
        h("p", { class: "kicker", text: t("add.outcome." + data.outcome, {}, data.outcome) }),
        h("h2", { class: "h-page", id: "added-h", text: visible(e.name || e.id) }),
        sourceLine(e)),
      h("span", { class: "pill you", text: e.source ? shortCommit(e.source.commit) : t("add.folderPill") })),
    n.length ? h("div", { class: "counts" }, n.map(([k, v]) => h("div", { class: "count" }, h("b", { text: String(v) }), h("span", { text: t("add.count." + k, { n: v }) })))) : null,
    packWarnings(data.warnings),
    data.outcome === "updated" ? notice("good", t("review.done", { commit: shortCommit(e.source && e.source.commit) })) : null,
    actions([
      h("a", { class: "btn primary", href: "#/plan/" + enc, text: t("packs.planInstall") }),
      h("a", { class: "btn", href: "#/packs", text: t("add.seePack") }),
      h("button", { class: "btn quiet", type: "button", text: t("add.another"), onclick: () => ctx.again() }),
    ], t("add.nextNote")));
}
