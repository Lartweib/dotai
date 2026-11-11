// The Installed board: what is installed in the home directory, as the
// install state says, with the install, reinstall and uninstall of a pack
// (each a plan or a preview first, then confirm), and the repositories
// where dotai apply ran, as the project records list them.

import { actions, boardHead, h, kv, lm, mount, sec } from "./dom.js";
import { confirmFlow, restoreHomeFlow, restoreProjectFlow } from "./flows.js";
import { fmtDate, fmtRelative, lower, t } from "./i18n.js";
import { harnessName, installed, registry, shared, tilde } from "./store.js";

const base = (p) => p.split(/[\\/]/).filter(Boolean).pop() || p;

export async function render(root, ctx) {
  const pane = h("div", { class: "detail solo" });
  mount(root, boardHead("installed"), pane);
  const s = installed();
  const home = h("div", { class: "stack" });
  const projects = (shared.status ? shared.status.projects : []).filter((p) => p.project);

  if (s) {
    const enc = encodeURIComponent(s.pack.path);
    const undo = h("div", { class: "out" });
    mount(home,
      h("div", { class: "meta" }, h("span", { class: "dot on" }), h("span", { class: "pill you", text: t("packs.installedMode", { mode: lower(t("mode." + s.mode)) }) }),
        h("span", { class: "t-meta", text: fmtRelative(s.installedAt) })),
      h("h2", { class: "h-page" }, s.pack.name + " ", h("span", { class: "ver", text: s.pack.version })),
      kv([
        [t("installed.k.variant"), s.variant],
        [t("installed.k.mode"), t("mode." + s.mode)],
        [t("installed.k.harnesses"), (s.harnesses || []).map(harnessName).join(", ")],
        [t("installed.k.when"), fmtDate(s.installedAt)],
        [t("installed.k.pack"), tilde(s.pack.path), true],
      ]),
      actions([
        h("a", { class: "btn primary", href: "#/plan/" + enc, text: t("installed.reinstall") }),
        h("a", { class: "btn", href: "#/tree/" + enc, text: t("packs.seeFiles") }),
        h("button", { class: "btn", type: "button", text: t("installed.uninstall"), onclick: () => confirmFlow(undo, restoreHomeFlow("", ctx)) }),
      ], t("installed.uninstallNote")),
      undo);
  } else {
    const packs = registry().filter((p) => !p.error);
    mount(home,
      h("h2", { class: "h-detail", text: t("installed.noneTitle") }),
      h("p", { class: "body", text: t("installed.noneBody") }),
      packs.length ? h("ul", { class: "fls" }, packs.map((p) => h("li", { class: "fl" },
        h("span", { class: "name", text: p.name + " " + p.version }), h("span", { class: "t-path", text: tilde(p.path) }),
        h("a", { class: "btn sm primary end", href: "#/plan/" + encodeURIComponent(p.path), text: t("packs.planInstall") }))))
        : actions([h("a", { class: "btn primary", href: "#/packs", text: t("tree.goPacks") })], t("installed.noPacks")));
  }

  mount(pane, h("div", { class: "col wide" },
    sec(t("installed.homeSec"), "", home),
    sec(t("installed.reposSec"), t("installed.reposHint"),
      projects.length ? h("ul", { class: "repos" }, projects.map((p) => repoRow(p, ctx)))
        : h("p", { class: "quietp", text: t("installed.noRepos") }),
      actions([h("a", { class: "btn", href: "#/projects", text: t("installed.applyRepo") })], t("installed.applyRepoNote")))));
}

function repoRow(p, ctx) {
  const rec = p.record;
  const out = h("div", { class: "out" });
  return h("li", { class: "repo" },
    h("div", { class: "repo-main" },
      h("div", { class: "title" }, h("b", { text: base(p.project) }), rec ? rec.profiles.map((x) => lm(x)) : null,
        rec ? h("span", { class: "t-meta", title: fmtDate(rec.appliedAt), text: t("projects.lastApplied", { when: fmtRelative(rec.appliedAt) }) }) : null),
      h("span", { class: "t-path", text: tilde(p.project) }),
      p.error ? h("span", { class: "err", text: p.error }) : null),
    h("div", { class: "btns" },
      h("a", { class: "btn sm", href: "#/projects/" + encodeURIComponent(p.project), text: t("installed.openRepo") }),
      rec ? h("button", { class: "btn sm", type: "button", text: t("projects.restorePreview"), onclick: () => confirmFlow(out, restoreProjectFlow(p.project, ctx)) }) : null),
    out);
}
