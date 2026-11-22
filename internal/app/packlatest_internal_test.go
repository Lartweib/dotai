package app

import "testing"

func TestLatestReleaseTagOrdersNumbersAsNumbers(t *testing.T) {
	const sha = "0123456789012345678901234567890123456789"
	list := func(tags ...string) string {
		out := ""
		for _, tag := range tags {
			out += sha + "\trefs/tags/" + tag + "\n"
		}
		return out
	}
	for name, tc := range map[string]struct {
		out  string
		want string
	}{
		"ten is above nine":                 {list("v1.9.0", "v1.10.0", "v1.2.0"), "refs/tags/v1.10.0"},
		"equal lengths compare by digit":    {list("v1.2.3", "v1.2.9", "v1.2.4"), "refs/tags/v1.2.9"},
		"the major decides first":           {list("v2.0.0", "v1.99.99"), "refs/tags/v2.0.0"},
		"a number beyond any integer":       {list("v1.0.0", "v99999999999999999999.0.0", "v2.0.0"), "refs/tags/v99999999999999999999.0.0"},
		"a huge minor against a huge minor": {list("v1.99999999999999999998.0", "v1.99999999999999999999.0", "v1.99999999999999999990.0"), "refs/tags/v1.99999999999999999999.0"},
		"junk is skipped":                   {list("v1.0.0", "v2.0.0-rc.1", "v3", "3.0.0", "v04.0.0", "v1.0.0+b"), "refs/tags/v1.0.0"},
		"carriage returns are ignored":      {sha + "\trefs/tags/v1.0.0\r\n", "refs/tags/v1.0.0"},
		"a line without a tab is skipped":   {"refs/tags/v9.0.0\n" + list("v1.0.0"), "refs/tags/v1.0.0"},
		"a branch is not a tag":             {sha + "\trefs/heads/v9.0.0\n" + list("v1.0.0"), "refs/tags/v1.0.0"},
	} {
		got, ok := latestReleaseTag(tc.out)
		if !ok || got != tc.want {
			t.Errorf("%s: latestReleaseTag() = %q, %v, want %q", name, got, ok, tc.want)
		}
	}
	for name, out := range map[string]string{"nothing": "", "only junk": list("v1.0.0-rc.1", "latest")} {
		if got, ok := latestReleaseTag(out); ok {
			t.Errorf("%s: latestReleaseTag() = %q, want no tag", name, got)
		}
	}
}

func TestCompareVersion(t *testing.T) {
	v := func(a, b, c string) [3]string { return [3]string{a, b, c} }
	for name, tc := range map[string]struct {
		a, b [3]string
		want int
	}{
		"equal":             {v("1", "2", "3"), v("1", "2", "3"), 0},
		"longer is higher":  {v("10", "0", "0"), v("9", "0", "0"), 1},
		"same length digit": {v("1", "2", "3"), v("1", "2", "4"), -1},
		"huge over small":   {v("99999999999999999999", "0", "0"), v("1", "0", "0"), 1},
		"huge patches":      {v("1", "0", "99999999999999999998"), v("1", "0", "99999999999999999999"), -1},
	} {
		if got := compareVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: compareVersion(%v, %v) = %d, want %d", name, tc.a, tc.b, got, tc.want)
		}
	}
}
