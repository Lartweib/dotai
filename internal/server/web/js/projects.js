// The Projects board: the projects dotai applied to, and the apply of a
// pack's profiles to a project, planned first and carried out only as
// planned, by the plan's digest. A project restores as a whole, through
// the same preview and confirm as on the Backups board.

import { busy, h, kv, lm, mount, notice, op, sec, warning, when } from "./dom.js";
import { confirmFlow, guard, rel, restoreProjectFlow, word } from "./flows.js";
import { act, harnessName, isAbsolute, loadPack, post, recentPacks, recentProjects, rememberProject, selectedPack, shared, tilde } from "./store.js";

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-labelledby": "pr-h" });
  const detail = h("section", { class: "detail", "aria-label": "Selected project" });
  mount(root, list, detail);
  const known = (shared.status ? shared.status.projects : []).filter((p) => p.project);
  const paths = [...new Set([...known.map((p) => p.project), ...recentProjects()])];
  ctx.setCount("projects", known.length);
  let cur = ctx.arg || paths[0] || "";

  const input = h("input", { class: "input", type: "text", placeholder: "/path/to/project", "aria-label": "Absolute path of a project", autocomplete: "off", spellcheck: "false" });
  const err = h("p", { class: "t-cap", role: "alert" });
  const form = h("form", { class: "field empty", onsubmit: (e) => {
    e.preventDefault();
    const v = input.value.trim();
    if (!isAbsolute(v)) {
      err.textContent = "Give the absolute path of the project directory.";
      return;
    }
    rememberProject(v);
    location.hash = "#/projects/" + encodeURIComponent(v);
  } }, h("div", { class: "row" }, input, h("button", { class: "btn", type: "submit", text: "Open" })), err);

  const draw = () => {
    mount(list,
      h("div", { class: "listhead" }, h("h1", { class: "h-list", id: "pr-h", text: "Projects" }), h("span", { class: "t-cap", text: known.length + " applied" })),
      form,
      h("ul", { class: "rows" }, paths.length ? paths.map((p) => {
        const st = known.find((k) => k.project === p);
        return h("li", {}, h("button", { class: "rowbtn", type: "button", "aria-pressed": String(p === cur), onclick: () => { cur = p; draw(); } },
          h("span", { class: "main" },
            h("span", { class: "title" }, h("b", { text: p.split(/[\\/]/).pop() || p }), st && st.record ? h("span", { class: "t-cap", text: st.record.pack.name }) : null),
            h("span", { class: "t-path", text: tilde(p) }),
            st && st.record ? h("span", { class: "pills" }, st.record.profiles.map((x) => h("span", { class: "pill sm", text: x }))) : null),
          h("span", { class: "when", text: st && st.record ? when(st.record.appliedAt) : "not applied" })));
      }) : h("li", { class: "t-cap empty", text: "No project yet. Open one by its path to apply a profile." })));
    if (cur) showProject(detail, cur, known.find((k) => k.project === cur), ctx);
    else mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: "No project" }),
      h("p", { class: "lead", text: "A project gets the profiles of a pack: rules, skills and mods that stay out of git, written to local files." })));
  };
  draw();
}

async function showProject(detail, project, st, ctx) {
  const rec = st && st.record;
  const packPath = rec ? rec.pack.path : selectedPack();
  const packs = [...new Set([packPath, ...recentPacks()].filter(Boolean))];
  const area = h("div");
  const restoreArea = h("div", { class: "sc stack" });
  mount(detail,
    h("div", { class: "col intro tight" },
      h("p", { class: "t-path", text: tilde(project) }),
      h("h2", { class: "h-detail", text: project.split(/[\\/]/).pop() || project }),
      st && st.error ? notice("bad", "record", st.error) : null),
    h("div", { class: "col" },
      rec ? sec("Applied", kv([["pack", rec.pack.name + " " + rec.pack.version + ", " + tilde(rec.pack.path)], ["profiles", rec.profiles.join(", ")], ["variant", rec.variant],
        ["harnesses", rec.harnesses.join(", ")], ["first apply", new Date(rec.firstApply).toUTCString()], ["last apply", new Date(rec.appliedAt).toUTCString()], ["record", tilde(st.recordPath)]])) : null,
      h("div", { class: "sec" }, h("h2", { text: "Apply" }), h("div", { class: "sc" }, area)),
      rec ? h("div", { class: "sec" }, h("h2", { text: "Restore" }), restoreArea) : null));
  if (rec) {
    mount(restoreArea, h("p", { class: "body", text: "Restore undoes every apply recorded here, so the project is as before the first apply." }),
      h("div", { class: "row" }, h("button", { class: "btn", type: "button", text: "Preview restore of the project", onclick: () => confirmFlow(restoreArea, restoreProjectFlow(project, ctx)) })));
  }
  if (!packs.length) {
    mount(area, h("p", { class: "body", text: "Add a pack on the Packs board first: its profiles are what an apply writes." }));
    return;
  }
  pickProfiles(area, project, packs, packPath, rec ? rec.profiles : [], ctx);
}

async function pickProfiles(area, project, packs, packPath, chosen, ctx) {
  mount(area, busy("Reading the pack..."));
  const info = await loadPack(packPath);
  if (ctx.stale()) return;
  const pack = info.data && info.data.pack;
  const select = h("select", { class: "input", "aria-label": "Pack", onchange: () => pickProfiles(area, project, packs, select.value, [], ctx) },
    packs.map((p) => h("option", { value: p, selected: p === packPath, text: tilde(p) })));
  if (!pack) {
    mount(area, select, notice("bad", info.error.code, info.error.message));
    return;
  }
  const boxes = pack.profiles.map((p) => {
    const b = h("input", { type: "checkbox", value: p.name });
    b.checked = chosen.includes(p.name);
    return h("label", { class: "check" }, b, h("span", {}, lm(p.name), " ", p.description || ""));
  });
  const planBtn = h("button", { class: "btn primary", type: "button", text: "Plan apply" });
  const out = h("div");
  planBtn.addEventListener("click", () => {
    const profiles = boxes.map((l) => l.querySelector("input")).filter((b) => b.checked).map((b) => b.value);
    planApply(out, { project, pack: packPath, profiles }, ctx);
  });
  mount(area, h("div", { class: "field" }, h("span", { class: "t-label", text: "Pack" }), select),
    pack.profiles.length ? h("fieldset", { class: "field" }, h("legend", { class: "t-label", text: "Profiles" }), boxes) : h("p", { class: "quietp", text: "This pack has no profiles." }),
    h("div", { class: "row" }, planBtn), h("p", { class: "t-cap", text: "Plan apply lists every file it would write in the project; nothing is written until you confirm." }), out);
}

async function planApply(out, req, ctx, msg) {
  mount(out, busy("Planning the apply: dotai stages the pack, runs rulesync and asks git, nothing is written..."));
  const res = await post("/api/apply/plan", req, ctx.signal);
  if (ctx.stale()) return;
  if (!res.data || !res.data.ops) {
    mount(out, notice("bad", res.error.code, res.error.message));
    return;
  }
  showPlan(out, req, res.data, ctx, msg);
}

function showPlan(out, req, p, ctx, msg) {
  const yes = h("input", { type: "checkbox" });
  const go = h("button", { class: "btn primary", type: "button", text: "Apply" });
  const result = h("div");
  // A plan with diagnostics comes without a digest: the page cannot act on it.
  const g = guard(go, () => !!p.digest && !p.blocked && (!p.consent || yes.checked));
  yes.addEventListener("change", g.sync);
  g.sync();
  go.addEventListener("click", async () => {
    mount(result, busy("Applying..."));
    const r = await g.run(() => act("/api/apply", { ...req, digest: p.digest, yes: yes.checked }));
    if (!r || ctx.stale()) return;
    if (r.ok) {
      rememberProject(req.project);
      await ctx.refresh();
      if (ctx.stale()) return;
      mount(out, notice("good", "applied", r.data.backup ? "Applied. The backup " + r.data.backup.id + " holds what the apply overwrote." : "Applied: nothing needed writing."),
        h("div", { class: "row" }, h("button", { class: "btn", type: "button", onclick: () => ctx.again(), text: "Show the project" })));
      return;
    }
    if (r.error.code === "plan-changed") {
      showPlan(out, req, r.data, ctx, notice("bad", r.error.code, r.error.message));
      return;
    }
    mount(result, notice("bad", r.error.code, r.error.message));
  });
  const harnesses = [...p.harnesses, ""];
  mount(out, msg || null,
    h("p", { class: "t-cap", text: [p.firstApply ? "first apply" : "applied before", "variant " + p.variant, p.profiles.join(", ")].join(" · ") }),
    harnesses.map((id) => {
      const ops = p.ops.filter((o) => (o.harness || "") === id);
      if (!ops.length) return null;
      const keep = ops.filter((o) => o.action === "unchanged").map((o) => rel(p.project, o.target));
      return h("section", { class: "sec" }, h("h2", { text: id ? harnessName(id) : "Project" }), h("div", { class: "sc" }, h("ul", {},
        ops.filter((o) => o.action !== "unchanged").map((o) => op(word(o.action), rel(p.project, o.target), "",
          (o.keys || []).length ? "Keys " + o.keys.map((k) => k.key.join(".") + " (" + word(k.action) + ")").join(", ") + "." : "")),
        keep.length ? op(word("unchanged"), keep.join(" · ")) : null)));
    }),
    p.exclude.entries.length ? sec("Exclude", h("p", { class: "t-path", text: p.exclude.file ? tilde(p.exclude.file) : "no git repository: the entries wait until there is one" }), h("pre", { class: "src", text: p.exclude.entries.join("\n") })) : null,
    sec("Warnings", notes.length ? h("ul", {}, notes) : h("p", { class: "quietp", text: "No warnings." })),
    h("div", { class: "confirmbar" },
      p.consent ? h("label", { class: "check" }, yes, h("span", { text: "Overwrite the conflicts: what dotai does not own or what changed since it wrote it. The backup keeps what they hold." })) : null,
      h("div", { class: "row" }, go),
      h("p", { class: "t-cap", text: p.blocked || !p.digest ? "The plan has problems or refusals: apply refuses it." : "Apply takes a backup in the project's backups, then writes." }),
      result));
}
