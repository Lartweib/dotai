// Small DOM helpers and the shared pieces of the boards. Every page is
// built with h(), which sets text with textContent, never HTML, so that a
// path or a message from the API can never become markup.

import { errorText, t } from "./i18n.js";

export function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "text") el.textContent = v;
    else el.setAttribute(k, v === true ? "" : String(v));
  }
  append(el, kids);
  return el;
}

function append(el, kids) {
  for (const kid of kids) {
    if (kid === undefined || kid === null || kid === false) continue;
    if (Array.isArray(kid)) append(el, kid);
    else el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
}

// mount replaces the children of el with kids.
export function mount(el, ...kids) {
  el.replaceChildren();
  append(el, kids);
}

const svgNS = "http://www.w3.org/2000/svg";

// icon draws one of the stroked icons of the design.
export function icon(name, size = 18) {
  const s = document.createElementNS(svgNS, "svg");
  const box = size === 16 ? 16 : 24;
  s.setAttribute("width", size);
  s.setAttribute("height", size);
  s.setAttribute("viewBox", `0 0 ${box} ${box}`);
  s.setAttribute("fill", "none");
  s.setAttribute("stroke", "currentColor");
  s.setAttribute("stroke-width", box === 16 ? "1.25" : "1.5");
  s.setAttribute("stroke-linecap", "round");
  s.setAttribute("stroke-linejoin", "round");
  s.setAttribute("aria-hidden", "true");
  const p = document.createElementNS(svgNS, "path");
  p.setAttribute("d", paths[name] || "");
  s.append(p);
  return s;
}

const paths = {
  packs: "M12 2.75l8.25 4.5v9.5L12 21.25l-8.25-4.5v-9.5z M3.75 7.25L12 11.75l8.25-4.5 M12 11.75v9.5 M7.9 5l8.25 4.5",
  installed: "M12 3.5v11 M7.5 10l4.5 4.5 4.5-4.5 M4 15.5v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3",
  home: "M3 11L12 3.5l9 7.5 M5.25 9.25v10a2 2 0 0 0 2 2h9.5a2 2 0 0 0 2-2v-10 M10 21.25V16.5a2 2 0 0 1 4 0v4.75",
  projects: "M8 7L3 12l5 5 M16 7l5 5-5 5 M14 4l-4 16",
  backups: "M19.25 13a7.25 7.25 0 1 1-14.5 0 7.25 7.25 0 0 1 14.5 0z M12 9.25V13l2.5 1.75 M3.25 6.25l3-2.75 M20.75 6.25l-3-2.75",
  doctor: "M12 21.25c-4.6-1.6-7.75-5-7.75-10V5.75L12 2.75l7.75 3v5.5c0 5-3.15 8.4-7.75 10z M8.75 12l2.25 2.25 4.25-4.5",
  doc: "M14 2.75H6.5a2 2 0 0 0-2 2v14.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V8.25z M14 2.75V7a1.25 1.25 0 0 0 1.25 1.25h4.25 M8.5 13h7 M8.5 17h5",
  sparkle: "M10.5 5.5c.6 4 3.9 7.4 8 8-4.1.6-7.4 4-8 8-.6-4-3.9-7.4-8-8 4.1-.6 7.4-4 8-8z M18.5 2.5c.25 1.5 1.5 2.75 3 3-1.5.25-2.75 1.5-3 3-.25-1.5-1.5-2.75-3-3 1.5-.25 2.75-1.5 3-3z",
  robot: "M6.75 8.5h10.5a2.5 2.5 0 0 1 2.5 2.5v7.25a2.5 2.5 0 0 1-2.5 2.5h-10.5a2.5 2.5 0 0 1-2.5-2.5v-7.25a2.5 2.5 0 0 1 2.5-2.5z M12 8.5V5.5 M10.25 17.25h3.5 M2.5 12.75v3.5 M21.5 12.75v3.5 M9 13v.5 M15 13v.5",
  server: "M5 3.5h14a2 2 0 0 1 2 2v3.5a2 2 0 0 1-2 2h-14a2 2 0 0 1-2-2v-3.5a2 2 0 0 1 2-2z M5 13h14a2 2 0 0 1 2 2v3.5a2 2 0 0 1-2 2h-14a2 2 0 0 1-2-2v-3.5a2 2 0 0 1 2-2z M13.5 7.25H17 M13.5 16.75H17",
  gear: "M9.95 5.31L10.35 2.64A9.5 9.5 0 0 1 13.65 2.64L14.05 5.31A7 7 0 0 1 16.77 6.88L19.28 5.89A9.5 9.5 0 0 1 20.93 8.75L18.82 10.43A7 7 0 0 1 18.82 13.57L20.93 15.25A9.5 9.5 0 0 1 19.28 18.11L16.77 17.12A7 7 0 0 1 14.05 18.69L13.65 21.36A9.5 9.5 0 0 1 10.35 21.36L9.95 18.69A7 7 0 0 1 7.23 17.12L4.72 18.11A9.5 9.5 0 0 1 3.07 15.25L5.18 13.57A7 7 0 0 1 5.18 10.43L3.07 8.75A9.5 9.5 0 0 1 4.72 5.89L7.23 6.88A7 7 0 0 1 9.95 5.31z M9.25 12a2.75 2.75 0 1 0 5.5 0a2.75 2.75 0 1 0 -5.5 0z",
  wrench: "M18.78 8.37L21.04 6.11A4.75 4.75 0 0 1 14.37 11.75L6.06 20.06A1.5 1.5 0 0 1 3.94 17.94L12.25 9.63A4.75 4.75 0 0 1 17.89 2.96L15.63 5.22z",
  file: "M14 2.75H6.5a2 2 0 0 0-2 2v14.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V8.25z M14 2.75V7a1.25 1.25 0 0 0 1.25 1.25h4.25",
  chev: "M6 9l6 6 6-6",
  warn: "M12 3.5L21.5 20h-19z M12 10v4.5 M12 17.25v.25",
  plus: "M8 3.5 V12.5 M3.5 8 H12.5",
  sun: "M8 5.25 A2.75 2.75 0 1 0 8 10.75 A2.75 2.75 0 1 0 8 5.25 M8 1.75 V3 M8 13 V14.25 M1.75 8 H3 M13 8 H14.25 M3.6 3.6 L4.45 4.45 M11.55 11.55 L12.4 12.4 M3.6 12.4 L4.45 11.55 M11.55 4.45 L12.4 3.6",
  moon: "M12.9 9.6 A5.25 5.25 0 1 1 6.4 3.1 A4.25 4.25 0 0 0 12.9 9.6 Z",
};


// Shared pieces of the boards.

// boardHead opens a board: its title and what it is for, in plain words.
export function boardHead(id, ...extra) {
  return h("header", { class: "boardhead" },
    h("h1", { class: "h-board", id: id + "-h", text: t(id + ".title") }),
    h("p", { class: "lead", text: t(id + ".lead") }),
    ...extra);
}

// sec is a section of a detail pane: a heading, an optional hint that
// explains its term, and its body.
export function sec(title, hintText, ...body) {
  return h("section", { class: "sec" }, h("h2", { text: title }),
    h("div", { class: "sc" }, hintText ? hint(hintText) : null, ...body));
}

export const hint = (text) => h("p", { class: "hint", text });

// kv is a list of labels and values; a pair [label, value, true] shows its
// value in the mono face, for paths, ids and codes.
export function kv(pairs) {
  return h("dl", { class: "kv" },
    pairs.filter(([, v]) => v !== undefined && v !== null && v !== "").map(([k, v, mono]) =>
      [h("dt", { text: k }), h("dd", { class: mono ? "mono" : null }, v)]));
}

// op is one line of a plan: what it does, to which path.
export function op(action, path, note, why) {
  return h("li", { class: "op" },
    h("span", { class: "tag", "data-a": action, text: t("action." + action) }),
    h("span", { class: "p", text: path }),
    note ? h("span", { class: "n", text: note }) : null,
    why ? h("span", { class: "why", text: why }) : null);
}

// tech folds what dotai said in its own words, in English, behind a
// summary, for the reader who wants it.
export function tech(detail) {
  return detail ? h("details", { class: "tech" }, h("summary", { text: t("common.tech") }), h("p", { class: "mono-text", text: detail })) : null;
}

// warning is one note of a plan: its title in the chosen language, from
// its code when the page knows it, and what dotai said as the detail.
export function warning(code, text, detail) {
  return h("li", { class: "wn" }, icon("warn", 16),
    h("span", { class: "wt" }, code ? h("span", { class: "label", text: t("code." + code, {}, code) }) : null,
      text ? h("span", { class: "msg", text }) : null, tech(detail)));
}

// notice is a message box: kind is "", "bad" or "good"; extra is a
// sentence for people, detail what dotai said.
export function notice(kind, text, detail, extra) {
  return h("div", { class: "notice " + (kind || ""), role: kind === "bad" ? "alert" : "status" },
    h("span", { class: "msg", text }),
    extra ? h("span", { class: "nd", text: extra }) : null,
    tech(detail));
}

// failure shows an API error in the chosen language, with what the
// server said as its technical detail.
export function failure(error) {
  const e = errorText(error);
  return notice("bad", e.text, e.detail, e.extra);
}

export function busy(text) {
  return h("p", { class: "busy", role: "status", text });
}

// actions is the row of buttons of a form, with the note that explains
// the first one.
export function actions(buttons, note) {
  return h("div", { class: "actions" }, h("div", { class: "btns" }, buttons), note ? h("p", { class: "help", text: note }) : null);
}

// layerHue gives a layer chip a stable hue from its name.
export function layerHue(name) {
  if (name === "base") return "2";
  let n = 0;
  for (const c of name || "") n = (n * 31 + c.charCodeAt(0)) >>> 0;
  return String([0, 1, 3, 4][n % 4]);
}

export function lm(name) {
  return h("span", { class: "lm", "data-h": layerHue(name), text: name });
}
