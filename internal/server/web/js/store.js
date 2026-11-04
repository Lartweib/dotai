// What the pages share: the API client, the recent packs and the pieces
// of state every page reads. There is no pack registry yet (decision
// 0043): the user types the path of a pack, and the page remembers the
// recent ones in localStorage, in this browser only.

import { t } from "./i18n.js";

const tokenKey = "dotai.session";
const packsKey = "dotai.packs";
const selectedKey = "dotai.pack";
const projectsKey = "dotai.projects";

// api calls the API with the session token and returns the envelope with
// the HTTP status: { ok, status, data, error }. Every failure has that
// shape too: "aborted" when the page left the board, "session" when the
// session ended, "network" when dotai ui did not answer and "bad-response"
// when the answer is not an envelope. Their words are in js/i18n.js; the
// message of these four is only a detail.
export async function api(path, options = {}) {
  const headers = { ...(options.headers || {}), Authorization: "Bearer " + sessionStorage.getItem(tokenKey) };
  let body = options.body;
  if (body !== undefined && typeof body !== "string") {
    body = JSON.stringify(body);
    headers["Content-Type"] = "application/json";
  }
  let res;
  try {
    res = await fetch(path, { ...options, headers, body });
  } catch (err) {
    if (err.name === "AbortError") return failure(0, "aborted", "");
    return failure(0, "network", err.message);
  }
  if (res.status === 401) {
    return failure(401, "session", "");
  }
  try {
    return { ...(await res.json()), status: res.status };
  } catch {
    return failure(res.status, "bad-response", "HTTP " + res.status);
  }
}

function failure(status, code, message) {
  return { ok: false, status, error: { code, message } };
}

export const post = (path, body, signal) => api(path, { method: "POST", body, signal });

export const query = (path, params) => path + "?" + new URLSearchParams(params).toString();

// act sends an action, which writes. When no envelope comes back, the
// action may have run all the same: maybeRan tells the message to say
// where to look.
export async function act(path, body) {
  const res = await post(path, body);
  if (res.error && (res.error.code === "network" || res.error.code === "bad-response")) {
    res.error.maybeRan = true;
  }
  return res;
}

function read(key, fallback) {
  try {
    const v = JSON.parse(localStorage.getItem(key));
    return v ?? fallback;
  } catch {
    return fallback;
  }
}

function write(key, value) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // Storage may be off; the page still works for this visit.
  }
}

const recentMax = 12;

export const recentPacks = () => read(packsKey, []).filter((p) => typeof p === "string");
export const recentProjects = () => read(projectsKey, []).filter((p) => typeof p === "string");

export function rememberPack(path) {
  write(packsKey, [path, ...recentPacks().filter((p) => p !== path)].slice(0, recentMax));
  selectPack(path);
}

export function forgetPack(path) {
  write(packsKey, recentPacks().filter((p) => p !== path));
  if (selectedPack() === path) selectPack(recentPacks()[0] || "");
}

export function rememberProject(path) {
  write(projectsKey, [path, ...recentProjects().filter((p) => p !== path)].slice(0, recentMax));
}

export const selectedPack = () => read(selectedKey, "") || recentPacks()[0] || "";
export const selectPack = (path) => write(selectedKey, path);

// isAbsolute accepts the absolute paths of every OS, as the API wants them.
export const isAbsolute = (p) => /^(\/|[A-Za-z]:[\\/]|\\\\)/.test(p);

// The state every page reads, loaded once and again after an action: the
// status, the packs read this visit and the names of the harnesses.
export const shared = { status: null, packs: new Map(), harnesses: new Map() };

export async function loadStatus() {
  const [res, hs] = await Promise.all([api("/api/status"), shared.harnesses.size ? null : api("/api/harnesses")]);
  shared.status = res.ok ? res.data : null;
  if (hs && hs.ok) for (const x of hs.data.harnesses) shared.harnesses.set(x.id, x.name);
  return res;
}

export const home = () => (shared.status ? shared.status.home : "");

// loadPack validates the pack at path, once per visit unless fresh.
export async function loadPack(path, fresh) {
  if (!fresh && shared.packs.has(path)) return shared.packs.get(path);
  const res = await api(query("/api/pack", { path }));
  if (res.ok || !["aborted", "network", "session", "bad-response"].includes(res.error.code)) shared.packs.set(path, res);
  return res;
}

const slash = (s) => (s || "").replace(/\\/g, "/").replace(/\/$/, "");

// tilde shortens a path under the home directory to ~/..., for reading.
export function tilde(p) {
  const h = slash(home());
  if (!p || !h) return p || "";
  const np = slash(p);
  if (np.toLowerCase() === h.toLowerCase()) return "~";
  if (np.toLowerCase().startsWith(h.toLowerCase() + "/")) return "~" + np.slice(h.length);
  return p;
}

// installed is the install state, or null when there is none.
export const installed = () => (shared.status && shared.status.state) || null;

// isInstalled reports whether the pack at path is the one installed.
export function isInstalled(path) {
  const s = installed();
  return !!s && samePath(s.pack.path, path);
}

export const samePath = (a, b) => slash(a).toLowerCase() === slash(b).toLowerCase();

// harnessName is the name dotai gives the harness id, the id itself for
// one it does not name, and the tools directory without one.
export const harnessName = (id) => (id ? shared.harnesses.get(id) || id : t("common.tools"));
