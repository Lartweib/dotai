// The Repositories board: the repositories where profiles were applied,
// as ~/.dotai/projects.json and the project records list them, each with
// its restore (restore --project, a preview first, then confirm). dotai's
// onboarding proposes profiles on its own, applied after the user
// approves the plan (decision 0045); applying by hand is the secondary path: a repository's path,
// a pack's profiles, the plan, then the apply, carried out only as
// planned, by the plan's digest.

import { actions, boardHead, busy, failure, h, hint, kv, lm, loading, mount, notice, op, sec, warning } from "./dom.js";
import { confirmFlow, guard, rel, restoreProjectFlow } from "./flows.js";
import { fmtDate, fmtRelative, t } from "./i18n.js";
import { act, harnessName, isAbsolute, loadPack, packPaths, post, recentProjects, rememberProject, selectedPack, shared, tilde } from "./store.js";

const base = (p) => p.split(/[\\/]/).filter(Boolean).pop() || p;
const href = (p) => "#/repos/" + encodeURIComponent(p);

// recorded lists the repositories the status knows of.
export const recorded = () => (shared.status ? shared.status.projects : []).filter((p) => p.project);

export async function render(root, ctx) {
  const pane = h("div", { class: "detail solo" });
  mount(root, boardHead("repos"), pane);
  const known = recorded();
  if (ctx.arg) {
    showRepo(pane, ctx.arg, known.find((k) => k.project === ctx.arg), ctx);
    return;
  }
  mount(pane, h("div", { class: "col wide" },
    sec(t("repos.listSec"), known.length ? t("repos.appliedHint") : "",
      known.length ? h("ul", { class: "repos" }, known.map((p) => repoRow(p, ctx)))
        : h("div", { class: "stack" }, h("p", { class: "body", text: t("repos.emptyBody") }), hint(t("hint.profile")))),
    byHand(t("repos.byHandNote"), (area) => mount(area, pathForm(known)))));
}

// byHand is the quiet way to apply profiles yourself: a fold that, the
// first time it opens, shows note and calls fill with the empty form area
// under it.
function byHand(note, fill) {
  const body = h("div", { class: "fold-body" });
  const el = h("details", { class: "fold manual" }, h("summary", { text: t("repos.byHand") }), body);
  el.addEventListener("toggle", () => {
    if (!el.open || body.dataset.filled) return;
    body.dataset.filled = "1";
    const area = h("div", { class: "form" });
    mount(body, h("p", { class: "help", text: note }), area);
    fill(area);
  });
  return el;
}

function repoRow(p, ctx) {
  const rec = p.record;
  const out = h("div", { class: "out" });
  return h("li", { class: "repo" },
    h("div", { class: "repo-main" },
      h("div", { class: "title" }, h("b", { text: base(p.project) }),
        rec ? h("span", { class: "t-meta", text: rec.pack.name }) : null, rec ? rec.profiles.map((x) => lm(x)) : null,
        rec ? h("span", { class: "t-meta", title: fmtDate(rec.appliedAt), text: t("repos.lastApplied", { when: fmtRelative(rec.appliedAt) }) }) : null),
      h("span", { class: "t-path", text: tilde(p.project) }),
      p.error ? h("span", { class: "err", text: p.error }) : null),
    h("div", { class: "btns" },
      h("a", { class: "btn sm", href: href(p.project), text: t("repos.open") }),
      rec ? h("button", { class: "btn sm", type: "button", text: t("repos.restorePreview"), onclick: () => confirmFlow(out, restoreProjectFlow(p.project, ctx)) }) : null),
    out);
}

// pathForm opens a repository by its path, for an apply by hand, with the
// paths opened here before that have no record yet.
function pathForm(known) {
  const recent = recentProjects().filter((p) => !known.some((k) => k.project === p));
  const input = h("input", { class: "input mono", id: "project-path", type: "text", placeholder: t("repos.placeholder"), autocomplete: "off", spellcheck: "false", "aria-describedby": "project-help" });
  const err = h("p", { class: "err", role: "alert" });
  return [h("form", { class: "form", onsubmit: (e) => {
    e.preventDefault();
    const v = input.value.trim();
    if (!isAbsolute(v)) {
      err.textContent = t("repos.notAbsolute");
      input.setAttribute("aria-invalid", "true");
      return;
    }
    rememberProject(v);
    location.hash = href(v);
  } }, h("div", { class: "field" },
    h("label", { class: "label", for: "project-path", text: t("repos.pathLabel") }),
    h("div", { class: "inline" }, input, h("button", { class: "btn", type: "submit", text: t("repos.open") })),
    h("p", { class: "help", id: "project-help", text: t("repos.pathHelp") }), err)),
  recent.length ? h("div", { class: "field" }, h("p", { class: "label", text: t("repos.recent") }),
    h("ul", { class: "paths" }, recent.map((p) => h("li", {}, h("a", { class: "t-path", href: href(p), text: tilde(p) }))))) : null];
}

function showRepo(pane, project, st, ctx) {
  const rec = st && st.record;
  const packPath = rec ? rec.pack.path : selectedPack();
  const packs = [...new Set([packPath, ...packPaths()].filter(Boolean))];
  const restoreArea = h("div", { class: "stack" });
  const apply = (area) => {
    if (!packs.length) {
      mount(area, h("p", { class: "body", text: t("repos.noPack") }),
        actions([h("a", { class: "btn", href: "#/packs", text: t("tree.goPacks") })]));
      return;
    }
    pickProfiles(area, project, packs, packPath, rec ? rec.profiles : [], ctx);
  };
  const area = h("div", { class: "form" });
  mount(pane,
    h("div", { class: "col intro" },
      h("a", { class: "t-meta", href: "#/repos", text: t("repos.back") }),
      h("p", { class: "t-path", text: tilde(project) }),
      h("h2", { class: "h-detail", text: base(project) }),
      h("p", { class: "meta" }, rec ? h("span", { class: "pill you", text: t("repos.appliedPill") }) : h("span", { class: "pill state", text: t("repos.notApplied") }),
        rec ? h("span", { class: "t-meta", text: t("repos.lastApplied", { when: fmtRelative(rec.appliedAt) }) }) : null),
      st && st.error ? notice("bad", t("repos.recordError"), st.error) : null),
    h("div", { class: "col" },
      rec ? sec(t("repos.appliedSec"), "", kv([
        [t("repos.k.pack"), rec.pack.name + " " + rec.pack.version],
        [t("repos.k.packPath"), tilde(rec.pack.path), true],
        [t("repos.k.profiles"), rec.profiles.join(", ")],
        [t("repos.k.variant"), rec.variant],
        [t("repos.k.harnesses"), rec.harnesses.map(harnessName).join(", ")],
        [t("repos.k.first"), fmtDate(rec.firstApply)],
        [t("repos.k.last"), fmtDate(rec.appliedAt) + " · " + fmtRelative(rec.appliedAt)],
        [t("repos.k.record"), tilde(st.recordPath), true],
      ]), hint(t("hint.record"))) : null,
      rec ? sec(t("repos.restoreSec"), "", restoreArea) : null,
      rec ? byHand(t("repos.againNote"), apply) : sec(t("repos.applySec"), t("hint.profile"), h("p", { class: "help", text: t("repos.byHandNote") }), area)));
  if (rec) {
    mount(restoreArea, h("p", { class: "body", text: t("repos.restoreBody") }),
      actions([h("button", { class: "btn", type: "button", text: t("repos.restorePreview"), onclick: () => confirmFlow(restoreArea, restoreProjectFlow(project, ctx)) })], t("repos.restoreNote")));
  } else {
    apply(area);
  }
}

async function pickProfiles(area, project, packs, packPath, chosen, ctx) {
  loading(area, t("repos.reading"));
  const info = await loadPack(packPath);
  if (ctx.stale()) return;
  const pack = info.data && info.data.pack;
  const select = h("select", { class: "input select", id: "apply-pack", onchange: () => pickProfiles(area, project, packs, select.value, [], ctx) },
    packs.map((p) => h("option", { value: p, selected: p === packPath, text: tilde(p) })));
  const packField = h("div", { class: "field" }, h("label", { class: "label", for: "apply-pack", text: t("repos.pack") }), select);
  if (!pack) {
    mount(area, packField, failure(info.error));
    return;
  }
  const boxes = pack.profiles.map((p) => {
    const b = h("input", { type: "checkbox", value: p.name });
    b.checked = chosen.includes(p.name);
    return h("label", { class: "check" }, b, lm(p.name), h("span", { class: "check-text", text: p.description || "" }));
  });
  const planBtn = h("button", { class: "btn primary", type: "button", text: t("repos.plan") });
  const out = h("div", { class: "out" });
  const g = guard(planBtn, () => boxes.some((l) => l.querySelector("input").checked));
  for (const l of boxes) l.querySelector("input").addEventListener("change", g.sync);
  g.sync();
  planBtn.addEventListener("click", () => {
    const profiles = boxes.map((l) => l.querySelector("input")).filter((b) => b.checked).map((b) => b.value);
    planApply(out, { project, pack: packPath, profiles }, ctx);
  });
  mount(area, packField,
    pack.profiles.length ? h("fieldset", { class: "field" }, h("legend", { class: "label", text: t("repos.profiles") }), h("div", { class: "checks" }, boxes))
      : h("p", { class: "quietp", text: t("repos.noProfiles") }),
    actions([planBtn], t("repos.planNote")), out);
}

// applyFailure is the failure of an apply plan or of an apply: a refused
// selection of harnesses says where to change it, since an apply follows
// the install's choice and takes none of its own (decision 0047).
function applyFailure(error) {
  return error && error.code === "harness-refused"
    ? h("div", { class: "stack" }, failure(error), h("p", { class: "help", text: t("repos.harnessRefused") }))
    : failure(error);
}

async function planApply(out, req, ctx, msg) {
  mount(out, busy(t("repos.planning")));
  const res = await post("/api/apply/plan", req, ctx.signal);
  if (ctx.stale()) return;
  if (!res.data || !res.data.ops) {
    mount(out, applyFailure(res.error));
    return;
  }
  showPlan(out, req, res.data, ctx, msg);
}

function showPlan(out, req, p, ctx, msg) {
  const yes = h("input", { type: "checkbox" });
  const go = h("button", { class: "btn primary", type: "button", text: t("repos.apply") });
  const result = h("div", { class: "out" });
  // A plan with diagnostics comes without a digest: the page cannot act on it.
  const g = guard(go, () => !!p.digest && !p.blocked && (!p.consent || yes.checked));
  yes.addEventListener("change", g.sync);
  g.sync();
  go.addEventListener("click", async () => {
    mount(result, busy(t("repos.applying")));
    const r = await g.run(() => act("/api/apply", { ...req, digest: p.digest, yes: yes.checked }));
    if (!r || ctx.stale()) return;
    if (r.ok) {
      rememberProject(req.project);
      await ctx.refresh();
      if (ctx.stale()) return;
      mount(out, notice("good", r.data.backup ? t("repos.doneBackup", { id: r.data.backup.id }) : t("repos.doneNothing")),
        actions([h("button", { class: "btn", type: "button", onclick: () => ctx.again(), text: t("repos.showProject") })]));
      return;
    }
    if (r.error.code === "plan-changed") {
      showPlan(out, req, r.data, ctx, failure(r.error));
      return;
    }
    mount(result, applyFailure(r.error));
  });
  const notes = [
    ...(p.refusals || []).map((x) => warning(x.code, t("note." + x.code, { harness: harnessName(x.harness), item: x.item }, ""), x.message)),
    ...(p.warnings || []).map((x) => warning(x.code, t("note." + x.code, { harness: harnessName(x.harness), item: x.item }, ""), x.message)),
    (p.notices || []).length ? warning("notice", t("plan.notices", { n: p.notices.length }), p.notices.map((x) => (typeof x === "string" ? x : x.message)).join("\n")) : null,
  ].filter(Boolean);
  const harnesses = [...p.harnesses, ""];
  mount(out, h("div", { class: "planout" }, msg || null,
    h("p", { class: "summary", text: t(p.firstApply ? "repos.firstApply" : "repos.appliedBefore", { variant: p.variant, profiles: p.profiles.join(", ") }) }),
    harnesses.map((id) => {
      const ops = p.ops.filter((o) => (o.harness || "") === id);
      if (!ops.length) return null;
      const keep = ops.filter((o) => o.action === "unchanged").map((o) => rel(p.project, o.target));
      return h("div", { class: "sub" }, h("h3", { class: "h-sub", text: id ? harnessName(id) : t("repos.repoFiles") }), h("ul", { class: "list" },
        ops.filter((o) => o.action !== "unchanged").map((o) => op(o.action, rel(p.project, o.target), "",
          (o.keys || []).length ? t("plan.keys", { list: o.keys.map((k) => k.key.join(".") + " (" + t("action." + k.action) + ")").join(", ") }) : "")),
        keep.length ? op("unchanged", keep.join(" · ")) : null));
    }),
    p.exclude.entries.length ? h("div", { class: "sub" }, h("h3", { class: "h-sub", text: t("repos.exclude") }), hint(t("hint.exclude")),
      h("p", { class: "t-path", text: p.exclude.file ? tilde(p.exclude.file) : t("repos.excludePending") }), h("pre", { class: "src", text: p.exclude.entries.join("\n") })) : null,
    h("div", { class: "sub" }, h("h3", { class: "h-sub", text: t("plan.warnings") }), notes.length ? h("ul", { class: "list" }, notes) : h("p", { class: "quietp", text: t("repos.noWarnings") })),
    h("div", { class: "confirmbar" },
      p.consent ? h("label", { class: "check" }, yes, h("span", { class: "check-text", text: t("repos.consent") })) : null,
      actions([go], p.blocked || !p.digest ? t("repos.refused") : t("repos.willWrite")),
      result)));
}
