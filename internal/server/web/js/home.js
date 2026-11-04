// The Home board: what dotai is, what it supports and what it does not,
// its two scopes, who renders what, its install modes, and how the layers
// of a pack merge, with the example pack, and the flow from install to a
// set-up repository with the selected pack's onboarding agent, as the API
// reads it. The words are static copy from the docs (js/i18n.js); the
// harnesses and the agent come from the API.

import { actions, boardHead, h, lm, mount, sec } from "./dom.js";
import { t } from "./i18n.js";
import { installed, loadOutline, registry, selectedPack, shared, tilde } from "./store.js";

const list = (keys) => h("ul", { class: "facts" }, keys.map((k) => h("li", { text: t(k) })));

export async function render(root, ctx) {
  const pane = h("div", { class: "detail solo" });
  mount(root, boardHead("home"), pane);
  const harnesses = [...shared.harnesses.values()];
  const s = installed();
  const next = s ? ["#/installed", "home.next.installed"] : registry().length ? ["#/packs", "home.next.install"] : ["#/packs", "home.next.import"];
  const agent = h("div", { class: "stack" });
  mount(pane, h("div", { class: "col wide" },
    h("div", { class: "intro" }, actions([h("a", { class: "btn primary", href: next[0], text: t(next[1]) })], t(next[1] + "Note"))),
    sec(t("home.supports"), "",
      h("p", { class: "body", text: t("home.supportsBody", { list: harnesses.join(", ") }) }),
      list(["home.supports.1", "home.supports.2", "home.supports.3"])),
    sec(t("home.not"), "", list(["home.not.1", "home.not.2", "home.not.3"])),
    sec(t("home.scopes"), "",
      h("dl", { class: "kv" },
        h("dt", { text: t("home.scope.user") }), h("dd", { text: t("home.scope.userBody") }),
        h("dt", { text: t("home.scope.project") }), h("dd", { text: t("home.scope.projectBody") }))),
    sec(t("home.render"), "",
      h("dl", { class: "kv" },
        h("dt", { text: "rulesync" }), h("dd", { text: t("home.render.rulesync") }),
        h("dt", { text: "dotai" }), h("dd", { text: t("home.render.dotai") }))),
    sec(t("home.modes"), "",
      h("dl", { class: "kv" },
        h("dt", { text: t("mode.isolated") }), h("dd", { text: t("mode.isolatedDesc") }),
        h("dt", { text: t("mode.replace") }), h("dd", { text: t("mode.replaceDesc") })),
      h("p", { class: "hint", text: t("home.modes.shared") })),
    sec(t("home.levels"), t("home.levelsHint"),
      h("ol", { class: "levels" },
        level("base", "home.level.base"), level("variant", "home.level.variant"), level("web", "home.level.profile"), level("hello", "home.level.mod")),
      h("p", { class: "body", text: t("home.merge") }),
      list(["home.merge.1", "home.merge.2", "home.merge.3"])),
    sec(t("home.example"), t("home.exampleHint"),
      h("ul", { class: "facts" },
        h("li", {}, lm("base"), " ", t("home.example.base")),
        h("li", {}, lm("tmux"), " ", t("home.example.tmux")),
        h("li", {}, lm("web"), " ", t("home.example.web"))),
      h("p", { class: "body", text: t("home.example.result") })),
    sec(t("home.flow"), t("home.flowHint"),
      h("ol", { class: "steps" }, ["1", "2", "3", "4"].map((n) => h("li", { text: t("home.flow." + n) }))),
      agent)));
  const pack = selectedPack();
  const outline = pack ? await loadOutline(pack) : null;
  if (ctx.stale()) return;
  const o = outline && outline.ok ? outline.data.onboarding : null;
  if (o) {
    mount(agent, h("p", { class: "h-sub", text: t("home.agent.fromPack", { name: outline.data.name }) }),
      h("p", { class: "t-path", text: tilde(pack) + "/" + o.path }),
      h("pre", { class: "src agent", text: o.content }),
      h("p", { class: "hint", text: t("home.agent.adapt") }));
  } else {
    mount(agent, h("p", { class: "body", text: t(pack ? "home.agent.none" : "home.agent.noPack") }),
      h("p", { class: "t-path", text: "examples/minimal-pack/base/.rulesync/subagents/onboarding.md" }),
      h("p", { class: "hint", text: t("home.agent.adapt") }));
  }
}

function level(mark, key) {
  return h("li", { class: "level" }, h("span", { class: "level-head" }, lm(mark), h("b", { text: t(key) })), h("span", { class: "help", text: t(key + "Body") }));
}
