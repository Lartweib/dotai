// The dotai web UI (decision 0043): the session exchange, the shell and
// the router of its boards. The address dotai ui opens carries a one-time
// code in its fragment, which never reaches the server or a Referer
// header. This module takes the code out of the address, trades it once
// for the session token at /api/session, and keeps the token in
// sessionStorage, which belongs to this origin and this tab. Every API
// request sends the token as an Authorization: Bearer header (js/store.js).

import { failure, h, icon, mount } from "./js/dom.js";
import { languages, lang, setLang, t } from "./js/i18n.js";
import { api, loadStatus, registry, shared, tilde } from "./js/store.js";
import * as home from "./js/home.js";
import * as packs from "./js/packs.js";
import * as installed from "./js/installed.js";
import * as tree from "./js/tree.js";
import * as plan from "./js/plan.js";
import * as projects from "./js/projects.js";
import * as backups from "./js/backups.js";
import * as doctor from "./js/doctor.js";

const tokenKey = "dotai.session";
const themeKey = "dotai.theme";

function codeFromAddress() {
  const params = new URLSearchParams(location.hash.slice(1));
  const code = params.get("code");
  if (code) {
    history.replaceState(null, "", location.pathname + location.search);
  }
  return code;
}

async function exchange(code) {
  const res = await fetch("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  const envelope = await res.json();
  if (!envelope.ok) {
    throw envelope.error;
  }
  sessionStorage.setItem(tokenKey, envelope.data.token);
}

// The boards, by the route in the address: #/packs, #/tree and so on.
// The plan of an install, the files it writes and the apply to a project
// are steps of the Installed board.
const routes = {
  home: { view: home, nav: "home" },
  packs: { view: packs, nav: "packs" },
  installed: { view: installed, nav: "installed" },
  tree: { view: tree, nav: "installed" },
  plan: { view: plan, nav: "installed" },
  projects: { view: projects, nav: "installed" },
  backups: { view: backups, nav: "backups" },
  doctor: { view: doctor, nav: "doctor" },
};

const navItems = ["home", "packs", "installed", "backups", "doctor"];

const counts = {};

function route() {
  const [name, ...rest] = location.hash.replace(/^#\/?/, "").split("/");
  return { name: routes[name] ? name : "home", arg: decodeURIComponent(rest.join("/")) };
}

function renderNav() {
  const current = routes[route().name].nav;
  mount(document.getElementById("nav"), navItems.map((id) =>
    h("li", {}, h("a", { class: "nav-e", href: "#/" + id, "aria-current": id === current ? "page" : null },
      icon(id), h("span", { class: "nl", text: t("nav." + id) }),
      counts[id] ? h("span", { class: id === "doctor" && counts[id] > 0 ? "n you" : "n", text: String(counts[id]),
        "aria-label": t("nav.count." + id, { n: counts[id] }) }) : null))));
}

// setCount sets the number a board shows in the navigation. refresh sets
// them all after an action; the Doctor board sets its own, the one count
// only its check knows. A count of zero is not shown.
export function setCount(id, n) {
  counts[id] = n;
  renderNav();
}

let rendering = 0;
let leaving = null; // aborts the requests of the board shown

async function render() {
  const r = route();
  const root = document.getElementById("view");
  const turn = ++rendering;
  if (leaving) leaving.abort();
  leaving = new AbortController();
  const signal = leaving.signal;
  renderNav();
  root.className = "view";
  root.setAttribute("aria-labelledby", r.name + "-h");
  mount(root);
  try {
    await routes[r.name].view.render(root, { arg: r.arg, setCount, signal, stale: () => turn !== rendering, refresh, again: render });
  } catch (err) {
    if (turn === rendering) mount(root, h("div", { class: "detail solo" }, failure({ code: "internal", message: err.message })));
  }
  if (turn === rendering) document.title = t(r.name + ".title") + " · dotai";
}

// refresh reloads the shared state after an action and counts again.
export async function refresh() {
  await loadStatus();
  paintHome();
  counts.packs = registry().length;
  counts.installed = shared.status ? shared.status.projects.length + (shared.status.state ? 1 : 0) : 0;
  renderNav();
  api("/api/backups").then((res) => {
    if (res.ok) setCount("backups", res.data.home.length + res.data.projects.reduce((n, p) => n + p.backups.length, 0));
  });
}

// paintHome shows where dotai keeps its state: the directory of state.json.
function paintHome() {
  const el = document.getElementById("home");
  if (!shared.status) return;
  const dir = tilde(shared.status.statePath).replace(/[\\/][^\\/]*$/, "");
  el.textContent = dir;
  el.title = t("shell.homeTitle", { path: shared.status.statePath.replace(/[\\/][^\\/]*$/, "") });
}

const dark = () => {
  const th = document.documentElement.dataset.theme;
  return th ? th === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
};

function paintTheme() {
  const btn = document.getElementById("theme");
  btn.setAttribute("aria-label", t(dark() ? "shell.toLight" : "shell.toDark"));
  btn.title = btn.getAttribute("aria-label");
  mount(btn, icon(dark() ? "sun" : "moon", 16));
}

function setupTheme() {
  const btn = document.getElementById("theme");
  let stored = null;
  try {
    stored = localStorage.getItem(themeKey);
  } catch {
    // No storage: the theme follows the system.
  }
  if (stored === "dark" || stored === "light") document.documentElement.dataset.theme = stored;
  btn.addEventListener("click", () => {
    const next = dark() ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try {
      localStorage.setItem(themeKey, next);
    } catch {
      // The choice lasts for this visit only.
    }
    paintTheme();
  });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", paintTheme);
}

// paintShell writes the words of the header in the chosen language.
function paintShell() {
  document.getElementById("brand").setAttribute("aria-label", t("shell.brand"));
  document.getElementById("side").setAttribute("aria-label", t("shell.sections"));
  const group = document.getElementById("langs");
  group.setAttribute("aria-label", t("shell.language"));
  mount(group, languages.map((l) => h("button", { class: "lang", type: "button", lang: l.id, title: l.name, "aria-pressed": String(l.id === lang()), text: l.short,
    onclick: () => {
      if (l.id === lang()) return;
      setLang(l.id);
      paintShell();
      paintHome();
      renderNav();
      render();
    } })));
  paintTheme();
}

async function start() {
  setupTheme();
  paintShell();
  const root = document.getElementById("view");
  mount(root, h("p", { class: "busy", role: "status", text: t("shell.connecting") }));
  try {
    const code = codeFromAddress();
    if (code) {
      await exchange(code);
    }
    if (!sessionStorage.getItem(tokenKey)) {
      throw { code: "no-session", message: "" };
    }
    const version = await api("/api/version");
    if (!version.ok) {
      throw version.error;
    }
    document.getElementById("version").textContent = "dotai " + version.data.version;
  } catch (err) {
    mount(root, h("div", { class: "detail solo" }, h("div", { class: "col intro" },
      h("h1", { class: "h-detail", text: t("shell.noSessionTitle") }),
      failure(err && err.code ? err : { code: "network", message: String(err && err.message) }))));
    return;
  }
  await refresh();
  window.addEventListener("hashchange", render);
  render();
}

start();
