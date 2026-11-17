package app

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/lartweib/dotai/internal/pack"
)

// The source of a pack (decision 0044): a git repository on github.com,
// whose identity is github.com/<owner>/<repo>, or a local directory, whose
// identity is local/<name>.

const (
	// gitHubHost is the one git host dotai fetches packs from.
	gitHubHost = "github.com"
	// localPrefix starts the identity of a local pack.
	localPrefix = "local/"
)

// GitSource is a pack's repository as given to dotai pack add: URL, as the
// user wrote it, and ID, its identity, github.com/<owner>/<repo> in lower
// case.
type GitSource struct {
	URL string `json:"url"`
	ID  string `json:"id"`
}

// HostUnsupportedError refuses a git URL of a host other than github.com.
type HostUnsupportedError struct {
	URL, Host string
}

func (e *HostUnsupportedError) Error() string {
	return fmt.Sprintf("%s is on %s, and dotai fetches packs only from github.com: clone it yourself, then add the clone with dotai pack add --dir <path>", e.URL, e.Host)
}

var (
	// gitHubOwner is an owner: alphanumerics and single hyphens, neither
	// first nor last, at most 39 characters (GitHub's rule, not verified).
	gitHubOwner = regexp.MustCompile(`^[A-Za-z0-9]+(-[A-Za-z0-9]+)*$`)
	// gitHubRepo is a repository: alphanumerics, '-', '_' and '.'.
	gitHubRepo = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	// hexRef is a ref made of hexadecimal digits only, which may be a SHA.
	hexRef = regexp.MustCompile(`^[0-9a-fA-F]+$`)
	// urlScheme starts a URL with a scheme.
	urlScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)
	// scpLike is git's user@host:path, or host:path.
	scpLike = regexp.MustCompile(`^(?:([^@/:]+)@)?([^@/:]+):(.*)$`)
)

// ParsePackURL parses raw, one of https://github.com/<owner>/<repo>[.git],
// ssh://git@github.com/<owner>/<repo>[.git] and
// git@github.com:<owner>/<repo>[.git]. Another host is a
// *HostUnsupportedError; anything else, such as a local path or a URL with
// a port, a user other than git, a query or more path segments, is a usage
// error.
func ParsePackURL(raw string) (GitSource, error) {
	bad := func(why string) error {
		return &UsageError{msg: fmt.Sprintf("%q is not the URL of a GitHub repository: %s; give https://github.com/<owner>/<repo>, ssh://git@github.com/<owner>/<repo> or git@github.com:<owner>/<repo>, or add a directory with --dir", raw, why)}
	}
	if raw == "" {
		return GitSource{}, bad("it is empty")
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f || r == '\\' }) {
		return GitSource{}, bad("it holds a space, a control character or a backslash")
	}
	var path string
	switch {
	case urlScheme.MatchString(raw):
		scheme, rest, _ := strings.Cut(raw, "://")
		authority, p, ok := strings.Cut(rest, "/")
		if !ok {
			return GitSource{}, bad("it names no repository")
		}
		user, h, hasUser := strings.Cut(authority, "@")
		if !hasUser {
			h, user = user, ""
		}
		path = p
		if !strings.EqualFold(h, gitHubHost) {
			if strings.ContainsAny(h, ":[]") || h == "" {
				return GitSource{}, bad("its host is not a plain name")
			}
			return GitSource{}, &HostUnsupportedError{URL: raw, Host: h}
		}
		switch strings.ToLower(scheme) {
		case "https":
			if hasUser {
				return GitSource{}, bad("an https URL takes no user: dotai uses your git's credentials")
			}
		case "ssh":
			if user != "git" {
				return GitSource{}, bad("an ssh URL of GitHub has the user git")
			}
		default:
			return GitSource{}, bad("dotai fetches over https or ssh only")
		}
	case scpLike.MatchString(raw):
		m := scpLike.FindStringSubmatch(raw)
		user, h, p := m[1], m[2], m[3]
		if len(h) == 1 {
			return GitSource{}, bad("it is a Windows path")
		}
		if !strings.EqualFold(h, gitHubHost) {
			return GitSource{}, &HostUnsupportedError{URL: raw, Host: h}
		}
		if user != "git" {
			return GitSource{}, bad("an ssh URL of GitHub has the user git")
		}
		path = p
	default:
		return GitSource{}, bad("it is not a URL")
	}
	if strings.ContainsAny(path, "?#%") {
		return GitSource{}, bad("it holds a query, a fragment or an escape")
	}
	// One trailing slash, as a browser may copy it, is dropped.
	segs := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(segs) != 2 {
		return GitSource{}, bad("it needs exactly <owner>/<repo>")
	}
	owner, repo := segs[0], strings.TrimSuffix(segs[1], ".git")
	if problem := ownerProblem(owner); problem != "" {
		return GitSource{}, bad(fmt.Sprintf("the owner %q %s", owner, problem))
	}
	if problem := repoProblem(repo); problem != "" {
		return GitSource{}, bad(fmt.Sprintf("the repository %q %s", repo, problem))
	}
	return GitSource{URL: raw, ID: gitHubHost + "/" + strings.ToLower(owner) + "/" + strings.ToLower(repo)}, nil
}

// ownerProblem says why owner cannot name a GitHub owner dotai fetches
// from, "" when it can: it names a directory of ~/.dotai/packs too.
func ownerProblem(owner string) string {
	switch {
	case len(owner) > 39 || !gitHubOwner.MatchString(owner):
		return "must be letters, digits and single hyphens, at most 39"
	case pack.BadName(owner):
		return "is a device name Windows refuses as a folder"
	}
	return ""
}

// repoProblem says why repo cannot name a GitHub repository dotai fetches,
// "" when it can.
func repoProblem(repo string) string {
	switch {
	case repo == "" || len(repo) > 100 || !gitHubRepo.MatchString(repo):
		return "must be 1 to 100 letters, digits, '-', '_' and '.'"
	case repo == "." || repo == ".." || strings.HasSuffix(repo, "."):
		return "must not end with a dot"
	case pack.BadName(repo):
		return "is a device name Windows refuses as a folder"
	}
	return ""
}

// gitHubID checks that id is the identity of a GitHub pack, as ParsePackURL
// gives it, and returns its owner and repository.
func gitHubID(id string) (owner, repo string, ok bool) {
	rest, found := strings.CutPrefix(id, gitHubHost+"/")
	if !found {
		return "", "", false
	}
	owner, repo, found = strings.Cut(rest, "/")
	if !found || owner != strings.ToLower(owner) || repo != strings.ToLower(repo) || ownerProblem(owner) != "" || repoProblem(repo) != "" {
		return "", "", false
	}
	return owner, repo, true
}

// refForbidden are what git check-ref-format refuses in a ref name and
// what would make the ref a refspec or a revision expression.
var refForbidden = []string{":", "*", "?", "[", "\\", "^", "~", "..", "@{"}

// CheckPackRef refuses a ref dotai does not fetch: one that starts with '-',
// which git would read as an option, one with a space or a control
// character, and one with what git check-ref-format refuses, such as ':',
// which would make it a refspec. An empty ref is the default branch. A run
// of hexadecimal digits shorter than a full SHA passes: the fetch tells a
// tag or a branch of that name from an abbreviated SHA.
func CheckPackRef(ref string) error {
	switch {
	case strings.HasPrefix(ref, "-"):
		return &UsageError{msg: fmt.Sprintf("the ref %q starts with -: give a tag, a branch or a full commit SHA", ref)}
	case strings.ContainsFunc(ref, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return &UsageError{msg: fmt.Sprintf("the ref %q holds a space or a control character", ref)}
	}
	for _, bad := range refForbidden {
		if strings.Contains(ref, bad) {
			return &UsageError{msg: fmt.Sprintf("the ref %q holds %q, which a git ref name cannot: give a tag, a branch or a full commit SHA", ref, bad)}
		}
	}
	return nil
}

// fullSHA reports whether ref is a full commit SHA, SHA-1 or SHA-256.
func fullSHA(ref string) bool {
	return (len(ref) == 40 || len(ref) == 64) && hexRef.MatchString(ref)
}

// localID is the identity of the local pack named name.
func localID(name string) string { return localPrefix + name }

// localNameProblem says why name cannot name a new local pack, "" when it
// can: it follows the rule for mod names.
func localNameProblem(name string) string { return pack.ModNameProblem(name) }
