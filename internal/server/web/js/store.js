// What the pages share: the API client, the pack registry and the pieces
// of state every page reads. The registry lives in ~/.dotai/packs.json
// (decision 0043); this browser remembers only the selected pack and the
// projects opened here.

import { t } from "./i18n.js";

const tokenKey = "dotai.session";
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

export const recentProjects = () => read(projectsKey, []).filter((p) => typeof p === "string");

export function rememberProject(path) {
  write(projectsKey, [path, ...recentProjects().filter((p) => p !== path)].slice(0, recentMax));
}

// The packs of the registry, ~/.dotai/packs.json, as the server lists
// them: the page keeps only which one is selected, in this browser.
export const registry = () => (shared.registry ? shared.registry.packs : []);
export const packPaths = () => registry().map((p) => p.path);

export const selectedPack = () => {
  const paths = packPaths();
  const s = read(selectedKey, "");
  return paths.find((p) => samePath(p, s)) || paths[0] || "";
};
export const selectPack = (path) => write(selectedKey, path);

// loadRegistry reads the registry; a registry dotai cannot read is kept
// as registryError, for the Packs board to say so.
export async function loadRegistry() {
  const res = await api("/api/packs");
  shared.registryError = res.ok ? null : res.error && res.error.code === "registry-invalid" ? { ...res.error, data: res.data } : null;
  if (res.ok) shared.registry = res.data;
  return res;
}

// setRegistry keeps the registry an add, an update, an import or a
// removal answered with: its data holds the list, path and packs, beside
// what the command did.
export function setRegistry(data) {
  shared.registry = { path: data.path, packs: data.packs, recovered: data.recovered || [] };
}

// isAbsolute accepts the absolute paths of every OS, as the API wants them.
export const isAbsolute = (p) => /^(\/|[A-Za-z]:[\\/]|\\\\)/.test(p);

// The state every page reads, loaded once and again after an action: the
// status, the packs read this visit and the names of the harnesses.
export const shared = { status: null, registry: null, registryError: null, packs: new Map(), harnesses: new Map() };

export async function loadStatus() {
  const [res, hs] = await Promise.all([api("/api/status"), shared.harnesses.size ? null : api("/api/harnesses"), loadRegistry()]);
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

// loadOutline gives what each layer of the pack at path holds, by type.
export async function loadOutline(path) {
  const key = "outline:" + path;
  if (shared.packs.has(key)) return shared.packs.get(key);
  const res = await api(query("/api/pack/summary", { path }));
  if (res.ok) shared.packs.set(key, res);
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

// harnessName names the harness id the same way on every board, as the
// page's words say, falling back to the name dotai gives it; without an id
// it names the tools directory.
export const harnessName = (id) => (id ? t("harness." + id, {}, shared.harnesses.get(id) || id) : t("common.tools"));
