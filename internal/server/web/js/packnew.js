// The Create a pack board, the wizard of decision 0046: it asks the
// pack's folder, its name, the harnesses it is compatible with and those
// it requires (decision 0047), and its profiles,
// each with the description and hints that dotai's profiling skill knows
// it by, then shows the plan of dotai pack new: the files it writes from
// the template and the skill dotai will generate. The user ticks that
// they read it, and the plan is carried out by its digest; then the board
// lands on the new pack. The server fills in the template, checks the
// names and validates the pack; this board only collects the answers.

import { actions, boardHead, busy, failure, h, hint, kv, mount, notice, sec } from "./dom.js";
import { guard } from "./flows.js";
import { t } from "./i18n.js";
import { backLink, diagnostics } from "./packreview.js";
import { visible } from "./packsource.js";
import { act, harnessName, previewReason, isAbsolute, post, selectPack, setRegistry, shared } from "./store.js";
import { isPreview, wizardDefaults } from "./harnesspick.js";
import { emptyProfile, fileGroups, newRequest } from "./wizard.js";

export async function render(root, ctx) {
  const infos = shared.harnessInfo.length ? shared.harnessInfo : [...shared.harnesses.keys()].map((id) => ({ id, status: "supported" }));
  const state = { dir: "", name: "", ...wizardDefaults(infos), profiles: [emptyProfile()] };
  const out = h("div", { class: "flow-out" });
  const form = h("form", { class: "form" });
  const profiles = h("div", { class: "stack" });
  const err = h("div", { class: "err", role: "alert" });
  const preview = h("button", { class: "btn primary", type: "submit", text: t("new.preview") });

  // Any change to the answers drops a plan already shown: it is no longer
  // the plan of what the form holds.
  const changed = () => mount(out);

  const field = (id, label, help, input) => h("div", { class: "field" },
    h("label", { class: "label", for: id, text: label }), input, help ? h("p", { class: "help", id: id + "-help", text: help }) : null);
  const text = (id, value, onInput, props = {}) => {
    const el = h("input", { class: "input", id, type: "text", autocomplete: "off", spellcheck: "false", "aria-describedby": id + "-help", ...props });
    el.value = value;
    el.addEventListener("input", () => { onInput(el.value); changed(); });
    return el;
  };

  const drawProfiles = () => mount(profiles, state.profiles.map((p, i) => {
    const area = h("textarea", { class: "input", id: "new-p" + i + "-desc", rows: "2", "aria-describedby": "new-p" + i + "-desc-help" });
    area.value = p.description;
    area.addEventListener("input", () => { p.description = area.value; changed(); });
    return h("fieldset", { class: "sheet panel" },
      h("legend", { class: "label", text: t("new.profileN", { n: i + 1 }) }),
      field("new-p" + i + "-name", t("new.profileName"), t("new.profileNameHelp"), text("new-p" + i + "-name", p.name, (v) => { p.name = v; }, { class: "input mono" })),
      field("new-p" + i + "-desc", t("new.description"), t("new.descriptionHelp"), area),
      field("new-p" + i + "-hints", t("new.hints"), t("new.hintsHelp"), text("new-p" + i + "-hints", p.hints, (v) => { p.hints = v; }, { class: "input mono", placeholder: "package.json, *.config.js" })),
      field("new-p" + i + "-mods", t("new.mods"), t("new.modsHelp"), text("new-p" + i + "-mods", p.mods, (v) => { p.mods = v; }, { class: "input mono" })),
      h("button", { class: "btn quiet sm", type: "button", text: t("new.removeProfile"), onclick: () => {
        state.profiles.splice(i, 1);
        drawProfiles();
        changed();
      } }));
  }));

  // The compatible harnesses, every supported one ticked and a preview
  // one listed with its reason, unticked; then the required ones, none
  // ticked, each only for a compatible harness.
  const ids = infos.map((x) => x.id);
  const requireBoxes = h("div", { class: "stack tight" });
  const drawRequires = () => mount(requireBoxes, ids.map((id) => {
    const box = h("input", { type: "checkbox", id: "new-r-" + id });
    box.checked = state.requires.includes(id);
    box.disabled = !state.harnesses.includes(id);
    box.addEventListener("change", () => {
      state.requires = ids.filter((x) => document.getElementById("new-r-" + x).checked);
      changed();
    });
    return h("label", { class: "check", for: "new-r-" + id }, box, h("span", { class: "check-text", text: harnessName(id) }), h("span", { class: "mono", text: " " + id }));
  }));
  const harnessBoxes = h("div", { class: "stack tight" }, infos.map((x) => {
    const box = h("input", { type: "checkbox", id: "new-h-" + x.id });
    box.checked = state.harnesses.includes(x.id);
    box.addEventListener("change", () => {
      state.harnesses = ids.filter((id) => document.getElementById("new-h-" + id).checked);
      state.requires = state.requires.filter((id) => state.harnesses.includes(id));
      drawRequires();
      changed();
    });
    return h("div", {},
      h("label", { class: "check", for: "new-h-" + x.id }, box, h("span", { class: "check-text", text: harnessName(x.id) }), h("span", { class: "mono", text: " " + x.id }),
        isPreview(x) ? h("span", { class: "pill sm state", text: t("plan.harness.preview") }) : null),
      isPreview(x) && x.statusReason ? h("p", { class: "help", text: t("plan.previewReason", { reason: previewReason(x.id, x.statusReason) }) }) : null);
  }));
  drawRequires();

  const showPlan = (p, msg) => {
    const read = h("input", { type: "checkbox", id: "new-read" });
    const go = h("button", { class: "btn primary", type: "button", text: t("new.create") });
    const result = h("div", { class: "out" });
    const g = guard(go, () => !!p.digest && read.checked);
    read.addEventListener("change", () => g.sync());
    g.sync();
    go.addEventListener("click", async () => {
      mount(result, busy(t("new.creating")));
      const r = await g.run(() => act("/api/packs/new", { ...newRequest(state), digest: p.digest }));
      if (!r) return;
      if (r.ok) {
        setRegistry(r.data.added);
        selectPack(r.data.added.pack.path);
        await ctx.refresh();
        if (!ctx.stale()) location.hash = "#/packs";
        return;
      }
      if (ctx.stale()) return;
      if (r.error.code === "plan-changed" && r.data) {
        showPlan(r.data, failure(r.error));
        return;
      }
      mount(result, failure(r.error));
    });
    mount(out, h("section", { class: "sheet panel", "aria-labelledby": "new-plan-h" },
      msg,
      h("h2", { class: "h-sub", id: "new-plan-h", text: t("new.planTitle", { id: visible(p.id) }) }),
      kv([[t("new.folder"), visible(p.dir), true], [t("new.identity"), visible(p.id), true]]),
      sec(t("new.files", { n: p.files.length }), t("new.filesHint"),
        fileGroups(p.files).map((g) => h("div", { class: "stack tight" },
          h("p", { class: "kicker", text: g.folder ? g.folder + "/" : t("new.top") }),
          h("ul", { class: "facts mono" }, g.files.map((f) => h("li", { text: visible(f) })))))),
      p.warnings.length ? sec(t("plan.warnings"), "", diagnostics(p.warnings)) : null,
      sec(t("new.skill"), t("new.skillHint"), h("pre", { class: "src", text: p.skill })),
      h("label", { class: "check", for: "new-read" }, read, h("span", { class: "check-text", text: t("new.read") })),
      actions([go], t("new.createNote")),
      result));
  };

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    mount(err);
    const dir = form.querySelector("#new-dir");
    dir.removeAttribute("aria-invalid");
    if (!isAbsolute(state.dir.trim())) {
      err.textContent = t("packs.notAbsolute");
      dir.setAttribute("aria-invalid", "true");
      dir.focus();
      return;
    }
    preview.disabled = true;
    mount(out, busy(t("new.planning")));
    let res;
    try {
      res = await post("/api/packs/new/plan", newRequest(state), ctx.signal);
    } finally {
      preview.disabled = false;
    }
    if (ctx.stale()) return;
    if (res.ok) {
      showPlan(res.data);
      return;
    }
    mount(out, res.error.code === "invalid" && res.data && res.data.diagnostics
      ? h("div", { class: "stack" }, failure(res.error), diagnostics(res.data.diagnostics))
      : failure(res.error));
  });

  mount(form,
    field("new-dir", t("new.dir"), t("new.dirHelp"), text("new-dir", state.dir, (v) => { state.dir = v; }, { class: "input mono", placeholder: t("packs.placeholder") })),
    field("new-name", t("new.name"), t("new.nameHelp"), text("new-name", state.name, (v) => { state.name = v; }, { class: "input mono" })),
    h("fieldset", { class: "field" }, h("legend", { class: "label", text: t("new.harnesses") }), harnessBoxes, hint(t("new.harnessesHelp"))),
    h("fieldset", { class: "field" }, h("legend", { class: "label", text: t("new.requires") }), requireBoxes, hint(t("new.requiresHelp"))),
    h("fieldset", { class: "field" }, h("legend", { class: "label", text: t("new.profiles") }),
      hint(t("new.profilesHelp")), profiles,
      h("button", { class: "btn sm", type: "button", text: t("new.addProfile"), onclick: () => {
        state.profiles.push(emptyProfile());
        drawProfiles();
        changed();
      } })),
    h("hr", { class: "hr" }),
    h("div", { class: "formfoot" }, h("p", { class: "help", text: t("new.foot") }), preview),
    err);
  drawProfiles();
  mount(root, backLink(), boardHead("new"),
    h("div", { class: "flow" },
      notice("", t("new.about"), "", t("new.aboutMore")),
      h("section", { class: "sheet panel", "aria-label": t("new.formLabel") }, form),
      out));
  form.querySelector("#new-dir").focus();
}
