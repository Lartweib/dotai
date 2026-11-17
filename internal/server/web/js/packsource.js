// Pure helpers of the pack boards (decision 0044): the identity a GitHub
// URL would get, the checks of a ref before it is sent, and the reading of
// an update plan. No DOM and no import, so that webtest/ runs them under
// node. The server stays the authority: the identity here is a preview.

// gitIdentity is github.com/<owner>/<repo>, in lower case, of an https,
// ssh or scp-like URL of github.com, or "" for anything else.
export function gitIdentity(url) {
  const u = String(url || "").trim();
  const m = /^(?:https:\/\/github\.com\/|ssh:\/\/git@github\.com\/|git@github\.com:)([A-Za-z0-9](?:-?[A-Za-z0-9])*)\/([A-Za-z0-9._-]+?)(?:\.git)?\/?$/.exec(u);
  if (!m || m[2].endsWith(".")) return "";
  return ("github.com/" + m[1] + "/" + m[2]).toLowerCase();
}

// refProblem names what is wrong with ref for its kind, "" when nothing
// is: "default" takes no ref, "named" a tag or a branch, "commit" a full
// SHA, which is all git can fetch by commit.
export function refProblem(kind, ref) {
  const r = String(ref || "").trim();
  if (kind === "default") return "";
  if (kind === "commit") return /^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$/.test(r) ? "" : "commit-full";
  if (r === "") return "ref-empty";
  if (r.startsWith("-")) return "ref-dash";
  if (/\s/.test(r)) return "ref-space";
  return "";
}

// shortCommit is the commit as the command line prints it.
export const shortCommit = (sha) => String(sha || "").slice(0, 12);

// diffLines reads a unified diff into lines to show: each its kind (hunk,
// ctx, add, del or note), its sign and its text. The --- and +++ headers
// before the first hunk are left out, since the change names its path.
export function diffLines(diff) {
  const out = [];
  let inHunk = false;
  for (const line of String(diff || "").split("\n")) {
    if (line.startsWith("@@")) {
      inHunk = true;
      out.push({ kind: "hunk", sign: "", text: line });
    } else if (!inHunk) {
      continue;
    } else if (line.startsWith("+")) {
      out.push({ kind: "add", sign: "+", text: line.slice(1) });
    } else if (line.startsWith("-")) {
      out.push({ kind: "del", sign: "-", text: line.slice(1) });
    } else if (line.startsWith(" ")) {
      out.push({ kind: "ctx", sign: " ", text: line.slice(1) });
    } else if (line.startsWith("\\")) {
      out.push({ kind: "note", sign: "", text: line });
    }
  }
  return out;
}

// visible escapes in s what a page would not show as it is: C0 and C1
// controls, DEL and the invisible format characters (the bidirectional
// overrides and isolates, the zero-width space, joiners and marks, the
// line and paragraph separators, the word joiner, the Arabic letter mark
// and the byte order mark), which could hide or reorder what a path or a
// name shows. The server treats the same set as unsafe in a diff.
export function visible(s) {
  return String(s || "").replace(/[\u0000-\u001f\u007f-\u009f\u061c\u200b-\u200f\u2028\u2029\u202a-\u202e\u2060\u2066-\u2069\ufeff]/g, (c) => {
    const n = c.charCodeAt(0);
    return n < 0x100 ? "\\x" + n.toString(16).padStart(2, "0") : "\\u" + n.toString(16).padStart(4, "0");
  });
}

// modeChanged reports whether a change moved the file between two known
// modes, such as 100644 to 100755.
export const modeChanged = (c) => !!c.modeBefore && !!c.modeAfter && c.modeBefore !== c.modeAfter;

// tally counts the changes of an update plan by kind, the modes that
// changed and the entries shown by their hashes only.
export function tally(changes) {
  const n = { added: 0, modified: 0, removed: 0, modes: 0, binary: 0 };
  for (const c of changes || []) {
    if (Object.hasOwn(n, c.change)) n[c.change]++;
    if (modeChanged(c)) n.modes++;
    if (c.binary) n.binary++;
  }
  return n;
}

// shellQuote quotes s for a POSIX shell: in single quotes, each single
// quote of s closed, escaped and opened again.
const shellQuote = (s) => "'" + String(s).replace(/'/g, "'\\''") + "'";

// addCommand is the command line that adds the pack of url at ref, for a
// fetch that takes longer than the web UI allows, or "" when url is not a
// github.com repository, which the command would refuse too.
export function addCommand(url, ref) {
  const u = String(url || "").trim();
  if (!gitIdentity(u)) return "";
  const r = String(ref || "").trim();
  return "dotai pack add " + shellQuote(u) + (r ? " --ref " + shellQuote(r) : "");
}

// moves reports whether an update plan takes the pack to another commit,
// or fetches again a copy that was gone.
export const moves = (p) => p.from.commit !== p.to.commit || !!p.missing;

// shortDigest is the start of a plan's digest, enough to tell two apart.
export const shortDigest = (d) => String(d || "").replace(/^sha256:/, "").slice(0, 12);

// planNotes is what an update plan warns about, with the notices of its
// fetch that are outside the plan and its digest.
export const planNotes = (p) => [...(p.warnings || []), ...(p.notices || [])];

// failureKind sorts the failure of an add or an update for the page: a
// fetch stopped by the time limit ("timeout"), a request whose answer was
// lost, so that it may have run ("lost"), another operation that still
// runs ("busy"), an invalid pack with its diagnostics ("invalid"), a tree
// refused with its problems ("tree"), or any other failure ("plain").
export function failureKind(res) {
  const e = (res && res.error) || {};
  const data = (res && res.data) || {};
  if (e.code === "internal" && data.timedOut) return "timeout";
  if (e.maybeRan || e.code === "network" || e.code === "bad-response") return "lost";
  if (e.code === "busy") return "busy";
  if (e.code === "invalid" && Array.isArray(data.diagnostics)) return "invalid";
  if (e.code === "pack-tree-refused" && Array.isArray(data.problems)) return "tree";
  return "plain";
}
