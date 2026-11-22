// Where a long identity or path may wrap: after a slash, never inside a
// name. No DOM and no import, so that webtest/ runs it under node.

// pathParts cuts text after each "/" or "\", so that "github.com/o/r" is
// "github.com/", "o/", "r". The parts of a text with no separator come
// back as one. A part with a space in it is a sentence, not a name, and
// is marked free: it may wrap at its spaces like any text.
export function pathParts(text) {
  const s = String(text ?? "");
  return s.split(/(?<=[/\\])/).filter((p) => p !== "").map((p) => ({ text: p, free: /\s/.test(p) }));
}

// isPathy reports whether text has a separator to wrap after: a text with
// none is left as plain text.
export const isPathy = (text) => /[/\\]./.test(String(text ?? ""));
