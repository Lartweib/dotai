// The Doctor board: what dotai doctor found, by place, and the fix of the
// exclude blocks. Doctor gives each finding a code and whether --fix can
// fix it, and no severity, so the board orders and marks findings by
// those alone. The fix carries out only the findings the board shows, by
// their digest. The title and the next step of each code are words for
// people (js/i18n.js), not rules of the engine; the finding's own message
// comes from dotai.

import { actions, boardHead, busy, failure, h, kv, mount, notice, sec } from "./dom.js";
import { guard } from "./flows.js";
import { fmtTime, t } from "./i18n.js";
import { act, api, harnessName, tilde } from "./store.js";

const records = new Set(["orphan-entry", "orphan-record", "state-invalid"]);

const titleOf = (f) => t("doctor.code." + f.code + ".title", {}, f.code);
const nextOf = (f) => t("doctor.code." + f.code + ".next", {}, t("doctor.nextUnknown"));

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-label": t("doctor.listLabel") });
  const detail = h("section", { class: "detail", "aria-label": t("doctor.detailLabel") });
  mount(root, boardHead("doctor"), list, detail);
  mount(list, busy(t("doctor.checking")));
  const res = await api("/api/doctor", { signal: ctx.signal });
  if (ctx.stale()) return;
  if (!res.data || !res.data.findings) {
    mount(list, failure(res.error));
    return;
  }
  draw(list, detail, res.data, ctx, null, fmtTime(new Date()));
}

function draw(list, detail, d, ctx, msg, checked) {
  const findings = d.findings.map((f, i) => ({ ...f, i }));
  const open = findings.filter((f) => !f.fixed);
  const fixable = open.filter((f) => f.fixable);
  ctx.setCount("doctor", open.length);
  const groups = new Map();
  for (const f of findings) {
    const g = f.project ? tilde(f.project) : records.has(f.code) ? t("doctor.records") : t("doctor.home");
    groups.set(g, [...(groups.get(g) || []), f]);
  }
  let cur = findings[0];
  const out = h("div", { class: "out" });
  const fix = h("button", { class: "btn primary", type: "button", text: t("doctor.fix") });
  const g = guard(fix, () => fixable.length > 0 && !!d.digest);
  g.sync();
  fix.addEventListener("click", async () => {
    mount(out, busy(t("doctor.fixing")));
    const r = await g.run(() => act("/api/doctor/fix", { digest: d.digest }));
    if (!r || ctx.stale()) return;
    if (!r.ok && r.error.code === "plan-changed") {
      draw(list, detail, r.data, ctx, failure(r.error), fmtTime(new Date()));
      return;
    }
    // A fix that ran, all of it or partway, gives the findings it got to.
    const ran = r.data && r.data.findings;
    const fixed = ran ? r.data.findings.filter((f) => f.fixed).length : 0;
    const told = r.ok || (r.error.code === "invalid" && ran)
      ? notice("good", t("doctor.fixed", { n: fixed }))
      : failure({ ...r.error, message: r.error.message + (ran ? " " + t("doctor.fixedBefore", { n: fixed }) : "") });
    if (!ran) {
      mount(out, told);
      return;
    }
    await ctx.refresh();
    if (ctx.stale()) return;
    const again = await api("/api/doctor", { signal: ctx.signal });
    if (ctx.stale()) return;
    if (again.data && again.data.findings) {
      draw(list, detail, again.data, ctx, told, fmtTime(new Date()));
      return;
    }
    mount(out, told, failure(again.error));
  });

  const rows = () => [...groups].map(([label, fs]) => h("div", { class: "group" },
    h("div", { class: "gh" }, h("h2", { text: label }), h("span", { class: "cnt", text: String(fs.length) })),
    h("ul", { class: "rows" }, fs.map((f) => h("li", {},
      h("button", { class: "rowbtn", type: "button", "aria-pressed": String(f === cur), onclick: () => { cur = f; paint(); } },
        h("span", { class: "main" },
          h("span", { class: "title" }, h("b", { text: titleOf(f) }),
            f.fixed ? h("span", { class: "pill sm state", text: t("doctor.fixedPill") }) : f.fixable ? h("span", { class: "pill sm fix", text: t("doctor.fixable") }) : null),
          h("span", { class: "t-path", text: where(f) }))))))));

  const paint = () => {
    mount(list,
      h("div", { class: "listhead stack" },
        h("p", { class: "t-meta", text: t("doctor.status", { n: open.length, f: fixable.length, time: checked }) }),
        actions([open.length ? fix : null, h("button", { class: "btn", type: "button", onclick: () => ctx.again(), text: t("doctor.again") })].filter(Boolean),
          fixable.length ? t("doctor.fixNote", { n: new Set(fixable.map((f) => f.project)).size }) : open.length ? t("doctor.nothingFixable") : ""),
        msg, out),
      findings.length ? rows() : h("p", { class: "help empty", text: t("doctor.clean") }));
    showFinding(detail, cur, d);
  };
  paint();
}

function where(f) {
  const p = f.path ? tilde(f.path) : f.project ? tilde(f.project) : "";
  return f.key && f.key.length ? p + " · " + f.key.join(".") : p;
}

function showFinding(detail, f, d) {
  if (!f) {
    mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: t("doctor.allClear") }),
      h("p", { class: "body", text: d.state ? t("doctor.allClearBody", { n: d.projects.length }) : d.projects.length ? t("doctor.allClearNoState", { n: d.projects.length }) : t("doctor.allClearEmpty") })));
    return;
  }
  mount(detail,
    h("div", { class: "col intro" },
      h("p", { class: "meta" }, h("span", { class: "pill state mono", text: f.code }),
        f.fixed ? h("span", { class: "pill fix", text: t("doctor.fixedPill") }) : f.fixable ? h("span", { class: "pill fix", text: t("doctor.fixable") }) : h("span", { class: "t-meta", text: t("doctor.notFixable") })),
      h("h2", { class: "h-detail", text: titleOf(f) }),
      h("p", { class: "body", text: nextOf(f) })),
    h("div", { class: "col" },
      sec(t("doctor.where"), "", kv([[t("doctor.k.file"), tilde(f.path), true], [t("doctor.k.key"), (f.key || []).join("."), true], [t("doctor.k.harness"), f.harness ? harnessName(f.harness) : ""], [t("doctor.k.project"), tilde(f.project), true],
        [t("doctor.k.recorded"), f.project ? t("doctor.projectRecord") : d.state ? tilde(d.state) : "", !f.project]])),
      sec(t("doctor.says"), t("doctor.saysHint"), h("p", { class: "quote", text: f.message }))));
}
