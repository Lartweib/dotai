// Session exchange of the dotai web UI (decision 0043). The address dotai
// ui opens carries a one-time code in its fragment, which never reaches
// the server or a Referer header. This module takes the code out of the
// address, trades it once for the session token at /api/session, and keeps
// the token in sessionStorage, which belongs to this origin and this tab.
// Every API request sends the token as an Authorization: Bearer header.

const tokenKey = "dotai.session";

function codeFromAddress() {
  const params = new URLSearchParams(location.hash.slice(1));
  const code = params.get("code");
  if (location.hash) {
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

export async function api(path, options = {}) {
  const token = sessionStorage.getItem(tokenKey);
  const headers = { ...(options.headers || {}), Authorization: "Bearer " + token };
  const res = await fetch(path, { ...options, headers });
  return res.json();
}

async function start() {
  const status = document.getElementById("status");
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
    status.textContent = "Connected to dotai " + version.data.version + ".";
  } catch (err) {
    status.textContent = err.message;
  }
}

start();
