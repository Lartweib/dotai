// The Projects board: the projects dotai applied to, and the apply of a
// pack's profiles to a project, planned first and carried out only as
// planned, by the plan's digest. A project restores as a whole, through
// the same preview and confirm as on the Backups board.

import { actions, boardHead, busy, failure, h, hint, kv, lm, mount, notice, op, sec, warning } from "./dom.js";
import { confirmFlow, guard, rel, restoreProjectFlow } from "./flows.js";
import { fmtDate, fmtRelative, t } from "./i18n.js";
import { act, harnessName, isAbsolute, loadPack, packPaths, post, recentProjects, rememberProject, selectedPack, shared, tilde } from "./store.js";

const base = (p) => p.split(/[\\/]/).filter(Boolean).pop() || p;

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-label": t("projects.listLabel") });
  const detail = h("section", { class: "detail", "aria-label": t("projects.detailLabel") });
  mount(root, boardHead("projects"), list, detail);
  const known = (shared.status ? shared.status.projects : []).filter((p) => p.project);
  const paths = [...new Set([...known.map((p) => p.project), ...recentProjects()])];
  let cur = ctx.arg || paths[0] || "";

  const input = h("input", { class: "input mono", id: "project-path", type: "text", placeholder: t("projects.placeholder"), autocomplete: "off", spellcheck: "false", "aria-describedby": "project-help" });
  const err = h("p", { class: "err", role: "alert" });
  const form = h("form", { class: "form addform", onsubmit: (e) => {
    e.preventDefault();
    const v = input.value.trim();
    if (!isAbsolute(v)) {
      err.textContent = t("projects.notAbsolute");
      input.setAttribute("aria-invalid", "true");
      return;
    }
    rememberProject(v);
    location.hash = "#/projects/" + encodeURIComponent(v);
  } }, h("div", { class: "field" },
    h("label", { class: "label", for: "project-path", text: t("projects.pathLabel") }),
    h("div", { class: "inline" }, input, h("button", { class: "btn", type: "submit", text: t("projects.open") })),
    h("p", { class: "help", id: "project-help", text: t("projects.pathHelp") }), err));

  const draw = () => {
    mount(list,
      h("div", { class: "listhead" }, h("span", { class: "t-meta", text: t("projects.applied", { n: known.length }) })),
      h("ul", { class: "rows" }, paths.length ? paths.map((p) => {
        const st = known.find((k) => k.project === p);
        const rec = st && st.record;
        return h("li", {}, h("button", { class: "rowbtn", type: "button", "aria-pressed": String(p === cur), onclick: () => { cur = p; draw(); } },
          h("span", { class: "main" },
            h("span", { class: "title" }, h("b", { text: base(p) })),
            h("span", { class: "t-path", text: tilde(p) }),
            rec ? h("span", { class: "pills" }, h("span", { class: "t-meta", text: rec.pack.name }), rec.profiles.map((x) => lm(x))) : null),
          h("span", { class: "when", title: rec ? fmtDate(rec.appliedAt) : null, text: rec ? fmtRelative(rec.appliedAt) : t("projects.notApplied") })));
      }) : h("li", { class: "help empty", text: t("projects.none") })),
      form);
    if (cur) showProject(detail, cur, known.find((k) => k.project === cur), ctx);
    else mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: t("projects.emptyTitle") }),
      h("p", { class: "body", text: t("projects.emptyBody") }), hint(t("hint.profile"))));
  };
  draw();
}

async function showProject(detail, project, st, ctx) {
  const rec = st && st.record;
  const packPath = rec ? rec.pack.path : selectedPack();
  const packs = [...new Set([packPath, ...packPaths()].filter(Boolean))];
  const area = h("div", { class: "form" });
  const restoreArea = h("div", { class: "stack" });
  mount(detail,
    h("div", { class: "col intro" },
      h("p", { class: "t-path", text: tilde(project) }),
      h("h2", { class: "h-detail", text: base(project) }),
      h("p", { class: "meta" }, rec ? h("span", { class: "pill you", text: t("projects.appliedPill") }) : h("span", { class: "pill state", text: t("projects.notApplied") }),
        rec ? h("span", { class: "t-meta", text: t("projects.lastApplied", { when: fmtRelative(rec.appliedAt) }) }) : null),
      st && st.error ? notice("bad", t("projects.recordError"), st.error) : null),
    h("div", { class: "col" },
      rec ? sec(t("projects.appliedSec"), "", kv([
        [t("projects.k.pack"), rec.pack.name + " " + rec.pack.version],
        [t("projects.k.packPath"), tilde(rec.pack.path), true],
        [t("projects.k.profiles"), rec.profiles.join(", ")],
        [t("projects.k.variant"), rec.variant],
        [t("projects.k.harnesses"), rec.harnesses.map(harnessName).join(", ")],
        [t("projects.k.first"), fmtDate(rec.firstApply)],
        [t("projects.k.last"), fmtDate(rec.appliedAt) + " · " + fmtRelative(rec.appliedAt)],
        [t("projects.k.record"), tilde(st.recordPath), true],
      ]), hint(t("hint.record"))) : null,
      sec(rec ? t("projects.applyAgain") : t("projects.applySec"), t("hint.profile"), area),
      rec ? sec(t("projects.restoreSec"), "", restoreArea) : null));
  if (rec) {
    mount(restoreArea, h("p", { class: "body", text: t("projects.restoreBody") }),
      actions([h("button", { class: "btn", type: "button", text: t("projects.restorePreview"), onclick: () => confirmFlow(restoreArea, restoreProjectFlow(project, ctx)) })], t("projects.restoreNote")));
  }
  if (!packs.length) {
    mount(area, h("p", { class: "body", text: t("projects.noPack") }),
      actions([h("a", { class: "btn", href: "#/packs", text: t("tree.goPacks") })]));
    return;
  }
  pickProfiles(area, project, packs, packPath, rec ? rec.profiles : [], ctx);
}

async function pickProfiles(area, project, packs, packPath, chosen, ctx) {
  mount(area, busy(t("projects.reading")));
  const info = await loadPack(packPath);
  if (ctx.stale()) return;
  const pack = info.data && info.data.pack;
  const select = h("select", { class: "input select", id: "apply-pack", onchange: () => pickProfiles(area, project, packs, select.value, [], ctx) },
    packs.map((p) => h("option", { value: p, selected: p === packPath, text: tilde(p) })));
  const packField = h("div", { class: "field" }, h("label", { class: "label", for: "apply-pack", text: t("projects.pack") }), select);
  if (!pack) {
    mount(area, packField, failure(info.error));
    return;
  }
  const boxes = pack.profiles.map((p) => {
    const b = h("input", { type: "checkbox", value: p.name });
    b.checked = chosen.includes(p.name);
    return h("label", { class: "check" }, b, lm(p.name), h("span", { class: "check-text", text: p.description || "" }));
  });
  const planBtn = h("button", { class: "btn primary", type: "button", text: t("projects.plan") });
  const out = h("div", { class: "out" });
  const g = guard(planBtn, () => boxes.some((l) => l.querySelector("input").checked));
  for (const l of boxes) l.querySelector("input").addEventListener("change", g.sync);
  g.sync();
  planBtn.addEventListener("click", () => {
    const profiles = boxes.map((l) => l.querySelector("input")).filter((b) => b.checked).map((b) => b.value);
    planApply(out, { project, pack: packPath, profiles }, ctx);
  });
  mount(area, packField,
    pack.profiles.length ? h("fieldset", { class: "field" }, h("legend", { class: "label", text: t("projects.profiles") }), h("div", { class: "checks" }, boxes))
      : h("p", { class: "quietp", text: t("projects.noProfiles") }),
    actions([planBtn], t("projects.planNote")), out);
}

async function planApply(out, req, ctx, msg) {
  mount(out, busy(t("projects.planning")));
  const res = await post("/api/apply/plan", req, ctx.signal);
  if (ctx.stale()) return;
  if (!res.data || !res.data.ops) {
    mount(out, failure(res.error));
    return;
  }
  showPlan(out, req, res.data, ctx, msg);
}

function showPlan(out, req, p, ctx, msg) {
  const yes = h("input", { type: "checkbox" });
  const go = h("button", { class: "btn primary", type: "button", text: t("projects.apply") });
  const result = h("div", { class: "out" });
  // A plan with diagnostics comes without a digest: the page cannot act on it.
  const g = guard(go, () => !!p.digest && !p.blocked && (!p.consent || yes.checked));
  yes.addEventListener("change", g.sync);
  g.sync();
  go.addEventListener("click", async () => {
    mount(result, busy(t("projects.applying")));
    const r = await g.run(() => act("/api/apply", { ...req, digest: p.digest, yes: yes.checked }));
    if (!r || ctx.stale()) return;
    if (r.ok) {
      rememberProject(req.project);
      await ctx.refresh();
      if (ctx.stale()) return;
      mount(out, notice("good", r.data.backup ? t("projects.doneBackup", { id: r.data.backup.id }) : t("projects.doneNothing")),
        actions([h("button", { class: "btn", type: "button", onclick: () => ctx.again(), text: t("projects.showProject") })]));
      return;
    }
    if (r.error.code === "plan-changed") {
      showPlan(out, req, r.data, ctx, failure(r.error));
      return;
    }
    mount(result, failure(r.error));
  });
  const notes = [
    ...(p.refusals || []).map((x) => warning(x.code, t("note." + x.code, { harness: harnessName(x.harness), item: x.item }, ""), x.message)),
    ...(p.warnings || []).map((x) => warning(x.code, t("note." + x.code, { harness: harnessName(x.harness), item: x.item }, ""), x.message)),
    (p.notices || []).length ? warning("notice", t("plan.notices", { n: p.notices.length }), p.notices.map((x) => (typeof x === "string" ? x : x.message)).join("\n")) : null,
  ].filter(Boolean);
  const harnesses = [...p.harnesses, ""];
  mount(out, h("div", { class: "planout" }, msg || null,
    h("p", { class: "summary", text: t(p.firstApply ? "projects.firstApply" : "projects.appliedBefore", { variant: p.variant, profiles: p.profiles.join(", ") }) }),
    harnesses.map((id) => {
      const ops = p.ops.filter((o) => (o.harness || "") === id);
      if (!ops.length) return null;
      const keep = ops.filter((o) => o.action === "unchanged").map((o) => rel(p.project, o.target));
      return h("div", { class: "sub" }, h("h3", { class: "h-sub", text: id ? harnessName(id) : t("projects.projectFiles") }), h("ul", { class: "list" },
        ops.filter((o) => o.action !== "unchanged").map((o) => op(o.action, rel(p.project, o.target), "",
          (o.keys || []).length ? t("plan.keys", { list: o.keys.map((k) => k.key.join(".") + " (" + t("action." + k.action) + ")").join(", ") }) : "")),
        keep.length ? op("unchanged", keep.join(" · ")) : null));
    }),
    p.exclude.entries.length ? h("div", { class: "sub" }, h("h3", { class: "h-sub", text: t("projects.exclude") }), hint(t("hint.exclude")),
      h("p", { class: "t-path", text: p.exclude.file ? tilde(p.exclude.file) : t("projects.excludePending") }), h("pre", { class: "src", text: p.exclude.entries.join("\n") })) : null,
    h("div", { class: "sub" }, h("h3", { class: "h-sub", text: t("plan.warnings") }), notes.length ? h("ul", { class: "list" }, notes) : h("p", { class: "quietp", text: t("projects.noWarnings") })),
    h("div", { class: "confirmbar" },
      p.consent ? h("label", { class: "check" }, yes, h("span", { class: "check-text", text: t("projects.consent") })) : null,
      actions([go], p.blocked || !p.digest ? t("projects.refused") : t("projects.willWrite")),
      result)));
}
