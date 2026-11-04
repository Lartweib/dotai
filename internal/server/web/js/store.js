// What the pages share: the API client, the recent packs and the pieces
// of state every page reads. There is no pack registry yet (decision
// 0043): the user types the path of a pack, and the page remembers the
// recent ones in localStorage, in this browser only.

const tokenKey = "dotai.session";
const packsKey = "dotai.packs";
const selectedKey = "dotai.pack";
const projectsKey = "dotai.projects";

// api calls the API with the session token and returns the envelope with
// the HTTP status: { ok, status, data, error }. A network failure is a
// failure envelope too, so that a page has one shape to show.
export async function api(path, options = {}) {
  const headers = { ...(options.headers || {}), Authorization: "Bearer " + sessionStorage.getItem(tokenKey) };
  let body = options.body;
  if (body !== undefined && typeof body !== "string") {
    body = JSON.stringify(body);
    headers["Content-Type"] = "application/json";
  }
  try {
    const res = await fetch(path, { ...options, headers, body });
    const env = await res.json();
    return { ...env, status: res.status };
  } catch (err) {
    return { ok: false, status: 0, error: { code: "network", message: "dotai ui did not answer: " + err.message } };
  }
}

export const post = (path, body) => api(path, { method: "POST", body });

export const query = (path, params) => path + "?" + new URLSearchParams(params).toString();

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

// The state every page reads, loaded once and again after an action.
export const shared = { status: null, packs: new Map(), home: "" };

export async function loadStatus() {
  const res = await api("/api/status");
  shared.status = res.ok ? res.data : null;
  if (res.ok && res.data.statePath) {
    shared.home = res.data.statePath.replace(/[\\/]\.dotai[\\/]state\.json$/, "");
  }
  return res;
}

// loadPack validates the pack at path, once per visit unless fresh.
export async function loadPack(path, fresh) {
  if (!fresh && shared.packs.has(path)) return shared.packs.get(path);
  const res = await api(query("/api/pack", { path }));
  shared.packs.set(path, res);
  return res;
}

// tilde shortens a path under the home directory to ~/..., for reading.
export function tilde(p) {
  const home = shared.home;
  if (!p || !home) return p || "";
  const norm = (s) => s.replace(/\\/g, "/");
  const np = norm(p);
  const nh = norm(home).replace(/\/$/, "");
  if (np === nh) return "~";
  if (np.toLowerCase().startsWith(nh.toLowerCase() + "/")) return "~" + np.slice(nh.length);
  return p;
}

// installed is the install state's pack, or null when there is none.
export const installed = () => shared.status?.state || null;

// isInstalled reports whether the pack at path is the one installed.
export function isInstalled(path) {
  const s = installed();
  return !!s && samePath(s.pack.path, path);
}

export const samePath = (a, b) => (a || "").replace(/\\/g, "/").replace(/\/$/, "").toLowerCase() === (b || "").replace(/\\/g, "/").replace(/\/$/, "").toLowerCase();

const harnessNames = { claudecode: "Claude Code", "antigravity-cli": "agy", opencode: "OpenCode" };

export const harnessName = (id) => (id ? harnessNames[id] || id : "Tools");
