// The dotai web UI (decision 0043): the session exchange, the shell and
// the router of its boards. The address dotai ui opens carries a one-time
// code in its fragment, which never reaches the server or a Referer
// header. This module takes the code out of the address, trades it once
// for the session token at /api/session, and keeps the token in
// sessionStorage, which belongs to this origin and this tab. Every API
// request sends the token as an Authorization: Bearer header (js/store.js).

import { h, icon, mount, notice } from "./js/dom.js";
import { api, loadStatus, recentPacks, shared, tilde } from "./js/store.js";
import * as packs from "./js/packs.js";
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
    throw new Error(envelope.error.message);
  }
  sessionStorage.setItem(tokenKey, envelope.data.token);
}

// The boards, by the route in the address: #/packs, #/tree and so on.
const routes = {
  packs: { view: packs, nav: "packs" },
  tree: { view: tree, nav: "tree" },
  plan: { view: plan, nav: "packs" },
  projects: { view: projects, nav: "projects" },
  backups: { view: backups, nav: "backups" },
  doctor: { view: doctor, nav: "doctor" },
};

const navItems = [
  ["packs", "Packs"],
  ["tree", "Installed"],
  ["projects", "Projects"],
  ["backups", "Backups"],
  ["doctor", "Doctor"],
];

const counts = {};

function route() {
  const [name, ...rest] = location.hash.replace(/^#\/?/, "").split("/");
  return { name: routes[name] ? name : "packs", arg: decodeURIComponent(rest.join("/")) };
}

function renderNav() {
  const current = routes[route().name].nav;
  mount(document.getElementById("nav"), navItems.map(([id, label]) =>
    h("li", {}, h("a", { class: "nav-e", href: "#/" + id, "aria-current": id === current ? "page" : null },
      icon(id), label,
      counts[id] !== undefined ? h("span", { class: id === "doctor" && counts[id] > 0 ? "n you" : "n", text: String(counts[id]) }) : null))));
}

// setCount sets the number a board shows in the navigation.
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
  mount(root);
  try {
    await routes[r.name].view.render(root, { arg: r.arg, setCount, signal, stale: () => turn !== rendering, refresh, again: render });
  } catch (err) {
    if (turn === rendering) mount(root, h("div", { class: "detail" }, notice("bad", "error", err.message)));
  }
}

// refresh reloads the shared state after an action and counts again.
export async function refresh() {
  await loadStatus();
  if (shared.status) document.getElementById("home").textContent = tilde(shared.status.statePath);
  counts.packs = recentPacks().length;
  counts.projects = shared.status ? shared.status.projects.length : 0;
  renderNav();
  api("/api/backups").then((res) => {
    if (res.ok) setCount("backups", res.data.home.length + res.data.projects.reduce((n, p) => n + p.backups.length, 0));
  });
}

function setupTheme() {
  const btn = document.getElementById("theme");
  const stored = () => {
    try {
      return localStorage.getItem(themeKey);
    } catch {
      return null;
    }
  };
  const dark = () => {
    const t = document.documentElement.dataset.theme;
    return t ? t === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  };
  const paint = () => {
    btn.setAttribute("aria-label", dark() ? "Switch to light theme" : "Switch to dark theme");
    mount(btn, icon(dark() ? "sun" : "moon", 16));
  };
  const t = stored();
  if (t === "dark" || t === "light") document.documentElement.dataset.theme = t;
  btn.addEventListener("click", () => {
    const next = dark() ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try {
      localStorage.setItem(themeKey, next);
    } catch {
      // The choice lasts for this visit only.
    }
    paint();
  });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", paint);
  paint();
}

async function start() {
  setupTheme();
  const root = document.getElementById("view");
  try {
    const code = codeFromAddress();
    if (code) {
      await exchange(code);
    }
    if (!sessionStorage.getItem(tokenKey)) {
      throw new Error("no session: open the address dotai ui printed");
    }
    const version = await api("/api/version");
    if (!version.ok) {
      throw new Error(version.error.message);
    }
    document.getElementById("version").textContent = "dotai " + version.data.version;
  } catch (err) {
    mount(root, h("div", { class: "detail" }, notice("bad", "session", err.message)));
    return;
  }
  await refresh();
  window.addEventListener("hashchange", render);
  render();
}

start();
