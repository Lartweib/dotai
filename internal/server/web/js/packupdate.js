// The Update board (decision 0044): pack update for the git pack of the
// route. The server fetches the recorded ref into a temporary folder and
// plans the change of executable content: hooks, MCP servers, settings,
// tools, mods and any executable file. The user reads it and confirms that
// plan by its digest; a fetch that differs by then, such as a branch that
// moved, comes back as the new plan to read again.

import { actions, boardHead, h, mount, notice } from "./dom.js";
import { t } from "./i18n.js";
import { backLink, fetchFailure, packWarnings, review, sourceLine, working } from "./packreview.js";
import { moves, planNotes, shortCommit, visible } from "./packsource.js";
import { act, isInstalled, post, registry, setRegistry } from "./store.js";

export async function render(root, ctx) {
  const id = ctx.arg;
  const entry = registry().find((p) => p.id === id);
  const status = h("div", { class: "flow-out" });
  const flow = h("div", { class: "flow" });
  mount(root, backLink(), boardHead("update"), flow);
  if (!entry || !entry.source) {
    mount(flow, h("section", { class: "sheet panel" },
      h("h2", { class: "h-detail", text: t(entry ? "update.localTitle" : "update.unknownTitle") }),
      h("p", { class: "body", text: t(entry ? "update.localBody" : "update.unknownBody", { id: visible(id) }) }),
      actions([h("a", { class: "btn primary", href: "#/packs", text: t("nav.packs") })])));
    return;
  }
  mount(flow, h("div", { class: "stack tight" }, h("h2", { class: "h-page", text: visible(entry.name || entry.id) }), sourceLine(entry)), status);
  const command = "dotai pack update " + id;

  const done = (r) => {
    const e = r.data.pack;
    mount(status, notice("good", t("review.done", { commit: shortCommit(e.source && e.source.commit) })),
      packWarnings(r.data.warnings),
      actions([
        h("a", { class: "btn primary", href: "#/plan/" + encodeURIComponent(e.path), text: t(isInstalled(e.path) ? "packs.reinstall" : "packs.planInstall") }),
        h("a", { class: "btn", href: "#/packs", text: t("nav.packs") }),
      ], t("update.doneNote")));
  };

  async function plan() {
    mount(status, working(t("update.planning")));
    const res = await post("/api/packs/update/plan", { id }, ctx.signal);
    if (ctx.stale()) return;
    if (!res.ok) {
      mount(status, fetchFailure(res, command), actions([h("button", { class: "btn", type: "button", text: t("common.tryAgain"), onclick: plan })]));
      return;
    }
    const p = res.data;
    if (!moves(p)) {
      mount(status, notice("good", t("update.upToDate", { commit: shortCommit(p.to.commit) })), packWarnings(planNotes(p)),
        actions([h("a", { class: "btn", href: "#/packs", text: t("nav.packs") })]));
      return;
    }
    review(status, p, {
      ctx, command, again: plan, busyText: t("review.updating"),
      confirm: (digest) => act("/api/packs/update", { id, digest }),
      apply: async (r) => {
        setRegistry(r.data);
        await ctx.refresh();
      },
      done,
    });
  }
  await plan();
}
