// The Doctor board: what dotai doctor found, by place, and the fix of the
// exclude blocks. Doctor gives each finding a code and whether --fix can
// fix it, and no severity, so the board orders and marks findings by
// those alone. The fix carries out only the findings the board shows, by
// their digest.

import { busy, h, kv, mount, notice, plural, sec } from "./dom.js";
import { guard } from "./flows.js";
import { act, api, harnessName, tilde } from "./store.js";

// What to do next, by code: copy for people, not a rule of the engine.
const next = {
  "file-changed": "Keep the edit in your pack instead, then install or apply again: dotai takes a backup of the edited file before it writes it.",
  "file-missing": "Install or apply again to write the file back.",
  "key-changed": "Set the value in your pack, then install or apply again; or keep your edit, which the next install overwrites after a backup.",
  "key-missing": "Apply the profile again to write the key back, or restore the project to remove what dotai applied there.",
  "exclude-missing": "Fix exclude entries writes the block back as the record lists it, after a backup of the exclude file in the project's backups. Entries other tools wrote stay.",
  "exclude-pending": "Fix exclude entries writes the block once the project is a git repository.",
  "orphan-entry": "Remove the keys the entry lists from ~/.claude.json, then the entry from projects.json. doctor does not do it for you.",
  "orphan-record": "The project directory is gone. Remove its record under ~/.dotai/projects by hand once you no longer need its backups.",
  "project-moved": "Apply again from the project's new top directory.",
  "install-interrupted": "The next install rolls back what the interrupted one wrote. Run an install or a restore.",
  "apply-interrupted": "The next apply or restore --project rolls back what the interrupted one wrote.",
  "state-invalid": "dotai cannot read its own records here. Restore from a backup, or move the file aside and install again.",
  "record-invalid": "The project record cannot be trusted. Restore the project, or move the record aside and apply again.",
  "check-failed": "Something doctor needed could not be read or asked, such as git. Check again once it answers.",
};

const records = new Set(["orphan-entry", "orphan-record", "state-invalid"]);

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-labelledby": "dr-h" });
  const detail = h("section", { class: "detail", "aria-label": "Selected finding" });
  mount(root, list, detail);
  mount(list, busy("Checking what dotai wrote against the disk..."));
  const res = await api("/api/doctor", { signal: ctx.signal });
  if (ctx.stale()) return;
  if (!res.data || !res.data.findings) {
    mount(list, notice("bad", res.error.code, res.error.message));
    return;
  }
  draw(list, detail, res.data, ctx, null, now());
}

// now is the time of a doctor run, as the list shows it.
const now = () => new Date().toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

function draw(list, detail, d, ctx, msg, checked) {
  const findings = d.findings.map((f, i) => ({ ...f, i }));
  const open = findings.filter((f) => !f.fixed);
  const fixable = open.filter((f) => f.fixable);
  ctx.setCount("doctor", open.length);
  const groups = new Map();
  for (const f of findings) {
    const g = f.project ? tilde(f.project) : records.has(f.code) ? "Records" : "Home";
    groups.set(g, [...(groups.get(g) || []), f]);
  }
  let cur = findings[0];
  const out = h("div");
  const fix = h("button", { class: "btn primary", type: "button", text: "Fix exclude entries" });
  const g = guard(fix, () => fixable.length > 0 && !!d.digest);
  g.sync();
  fix.addEventListener("click", async () => {
    mount(out, busy("Fixing..."));
    const r = await g.run(() => act("/api/doctor/fix", { digest: d.digest }));
    if (!r || ctx.stale()) return;
    if (!r.ok && r.error.code === "plan-changed") {
      draw(list, detail, r.data, ctx, notice("bad", r.error.code, r.error.message), now());
      return;
    }
    // A fix that ran, all of it or partway, gives the findings it got to.
    const ran = r.data && r.data.findings;
    const fixed = ran ? r.data.findings.filter((f) => f.fixed).length : 0;
    const told = r.ok || (r.error.code === "invalid" && ran)
      ? notice("good", "fixed", "Fixed " + plural(fixed, "finding") + ", each after a backup in its project's backups.")
      : notice("bad", r.error.code, r.error.message + (ran ? " Fixed " + plural(fixed, "finding") + " before it stopped." : ""));
    if (!ran) {
      mount(out, told);
      return;
    }
    await ctx.refresh();
    if (ctx.stale()) return;
    const again = await api("/api/doctor", { signal: ctx.signal });
    if (ctx.stale()) return;
    if (again.data && again.data.findings) {
      draw(list, detail, again.data, ctx, told, now());
      return;
    }
    mount(out, told, notice("bad", again.error.code, again.error.message));
  });

  const rows = () => [...groups].map(([label, fs]) => h("div", {},
    h("div", { class: "gh" }, h("h2", { text: label }), h("span", { class: "t-cap", text: String(fs.length) })),
    h("ul", { class: "rows" }, fs.map((f) => h("li", {},
      h("button", { class: "rowbtn", type: "button", "aria-pressed": String(f === cur), onclick: () => { cur = f; paint(); },
        "aria-label": f.code + (f.fixed ? ", fixed" : f.fixable ? ", fixable" : "") + ", " + where(f) },
        h("span", { class: "main" },
          h("span", { class: "title" }, h("span", { class: "code", text: f.code }),
            f.fixed ? h("span", { class: "pill sm state", text: "fixed" }) : f.fixable ? h("span", { class: "pill sm fix", text: "fixable" }) : null),
          h("span", { class: "t-path", text: where(f) }))))))));

  const paint = () => {
    mount(list,
      h("div", { class: "listhead stack" },
        h("div", { class: "row" }, h("h1", { class: "h-list", id: "dr-h", text: "Doctor" }),
          h("span", { class: "t-cap", text: plural(open.length, "problem") + " · " + fixable.length + " fixable · checked " + checked })),
        h("div", { class: "row" }, fix, h("button", { class: "btn quiet", type: "button", onclick: () => ctx.again(), text: "Check again" })),
        h("p", { class: "t-cap", text: fixable.length ? "Fix exclude entries rewrites the project's block in " + plural(new Set(fixable.map((f) => f.project)).size, "exclude file") + ", after a backup of each." : "Nothing here is fixable by doctor." }),
        msg, out),
      findings.length ? rows() : h("p", { class: "quietp empty", text: "doctor found nothing: what dotai recorded matches the disk." }));
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
    mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: "All clear" }),
      h("p", { class: "lead", text: d.state ? "The install state and " + plural(d.projects.length, "project record") + " match the disk." : "There is no install state to check, and " + plural(d.projects.length, "project record") + "." })));
    return;
  }
  mount(detail,
    h("div", { class: "col intro tight" },
      h("div", { class: "row" }, h("span", { class: "pill state", text: f.code }),
        f.fixed ? h("span", { class: "pill fix", text: "fixed" }) : f.fixable ? h("span", { class: "pill fix", text: "fixable" }) : h("span", { class: "t-cap", text: "not fixable by doctor" })),
      h("h2", { class: "h-detail", text: f.message })),
    h("div", { class: "col" },
      sec("Where", kv([["file", tilde(f.path)], ["key", (f.key || []).join(".")], ["harness", f.harness ? harnessName(f.harness) : ""], ["project", tilde(f.project)],
        ["recorded", f.project ? "the project record" : d.state ? tilde(d.state) : ""]], true)),
      sec("Next", h("p", { class: "body", text: next[f.code] || "Read the message above; doctor reports this and does not change it." }))));
}
