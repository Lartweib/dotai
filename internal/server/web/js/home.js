// The Home board: what dotai is and how a repository gets set up, from
// dotai install to the pack's onboarding agent and dotai apply, with the
// next step; then what it supports and does not, and, folded, its scopes,
// its install modes, who renders what, how the layers of a pack merge and
// the example pack. The words are static copy from the docs (js/i18n.js);
// the harnesses and the selected pack's onboarding agent come from the API.

import { actions, boardHead, failure, h, lm, mount, sec } from "./dom.js";
import { t } from "./i18n.js";
import { harnessName, installed, loadOutline, registry, selectedPack, shared, tilde } from "./store.js";

const list = (keys) => h("ul", { class: "facts" }, keys.map((k) => h("li", { text: t(k) })));

// fold is a section of the board that opens on demand.
const fold = (title, ...body) => h("details", { class: "fold" }, h("summary", { text: title }), h("div", { class: "fold-body" }, ...body));

// The lines of the agent's body the board shows: up to its first steps.
const excerptLines = 9;

export async function render(root, ctx) {
  const pane = h("div", { class: "detail solo" });
  mount(root, boardHead("home"), pane);
  const harnesses = [...shared.harnesses.keys()].map(harnessName);
  const s = installed();
  const next = s ? ["#/installed", "home.next.installed"] : registry().length ? ["#/packs", "home.next.install"] : ["#/packs", "home.next.import"];
  const agent = h("div", { class: "stack" });
  mount(pane, h("div", { class: "col wide" },
    sec(t("home.flow"), t("home.flowHint"),
      h("ol", { class: "steps" }, ["1", "2", "3", "4"].map((n) => h("li", { text: t("home.flow." + n) }))),
      actions([h("a", { class: "btn primary", href: next[0], text: t(next[1]) })], t(next[1] + "Note")),
      agent),
    sec(t("home.supports"), "",
      h("p", { class: "body", text: t("home.supportsBody", { list: harnesses.join(", ") }) }),
      list(["home.supports.1", "home.supports.2", "home.supports.3"])),
    sec(t("home.not"), "", list(["home.not.1", "home.not.2", "home.not.3"])),
    sec(t("home.more"), "",
      fold(t("home.scopes"),
        h("dl", { class: "kv" },
          h("dt", { text: t("home.scope.user") }), h("dd", { text: t("home.scope.userBody") }),
          h("dt", { text: t("home.scope.project") }), h("dd", { text: t("home.scope.projectBody") }))),
      fold(t("home.modes"),
        h("dl", { class: "kv" },
          h("dt", { text: t("mode.isolated") }), h("dd", { text: t("mode.isolatedDesc") }),
          h("dt", { text: t("mode.replace") }), h("dd", { text: t("mode.replaceDesc") })),
        h("p", { class: "hint", text: t("home.modes.shared") })),
      fold(t("home.render"),
        h("dl", { class: "kv" },
          h("dt", { text: "rulesync" }), h("dd", { text: t("home.render.rulesync") }),
          h("dt", { text: "dotai" }), h("dd", { text: t("home.render.dotai") }))),
      fold(t("home.levels"),
        h("p", { class: "hint", text: t("home.levelsHint") }),
        h("ol", { class: "levels" },
          level("base", "home.level.base"), level("variant", "home.level.variant"), level("web", "home.level.profile"), level("hello", "home.level.mod")),
        h("p", { class: "body", text: t("home.merge") }),
        list(["home.merge.1", "home.merge.2", "home.merge.3"])),
      fold(t("home.example"),
        h("p", { class: "hint", text: t("home.exampleHint") }),
        h("ul", { class: "facts" },
          h("li", {}, lm("base"), " ", t("home.example.base")),
          h("li", {}, lm("tmux"), " ", t("home.example.tmux")),
          h("li", {}, lm("web"), " ", t("home.example.web"))),
        h("p", { class: "body", text: t("home.example.result") })))));
  const pack = selectedPack();
  const outline = pack ? await loadOutline(pack) : null;
  if (ctx.stale()) return;
  if (outline && !outline.ok) {
    mount(agent, failure(outline.error));
    return;
  }
  const o = outline ? outline.data.onboarding : null;
  if (o) {
    mount(agent, h("p", { class: "h-sub", text: t("home.agent.fromPack", { name: outline.data.name }) }),
      h("pre", { class: "src", text: excerpt(o.content) }),
      h("p", { class: "src-path", text: t("home.agent.file", { path: tilde(pack) + "/" + o.path }) }),
      h("p", { class: "hint", text: t("home.agent.adapt") }));
  } else {
    mount(agent, h("p", { class: "body", text: t(pack ? "home.agent.none" : "home.agent.noPack") }),
      h("p", { class: "src-path", text: "examples/minimal-pack/base/.rulesync/subagents/onboarding.md" }),
      h("p", { class: "hint", text: t("home.agent.adapt") }));
  }
}

// excerpt is the start of the agent's instructions, after its frontmatter
// and the notes for the pack author.
function excerpt(content) {
  const body = content.replace(/^---[\s\S]*?\n---\s*\n/, "").replace(/<!--[\s\S]*?-->\s*/g, "").trim().split(/\r?\n/);
  return body.slice(0, excerptLines).join("\n") + (body.length > excerptLines ? "\n…" : "");
}

function level(mark, key) {
  return h("li", { class: "level" }, h("span", { class: "level-head" }, lm(mark), h("b", { text: t(key) })), h("span", { class: "help", text: t(key + "Body") }));
}
