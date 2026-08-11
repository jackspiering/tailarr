package upgrade

import (
	"strconv"
	"strings"
)

// Compare compares two SemVer strings using SemVer 2.0.0 precedence rules.
// A leading "v" (or "V") is optional and ignored, as is build metadata.
// Returns -1 when a < b, 0 when equal, +1 when a > b.
func Compare(a, b string) int {
	pa, aok := parse(a)
	pb, bok := parse(b)
	if !aok || !bok {
		// Fall back to plain string order for non-SemVer build tags.
		return strings.Compare(a, b)
	}
	if c := cmpInt(pa.major, pb.major); c != 0 {
		return c
	}
	if c := cmpInt(pa.minor, pb.minor); c != 0 {
		return c
	}
	if c := cmpInt(pa.patch, pb.patch); c != 0 {
		return c
	}
	return comparePre(pa.pre, pb.pre)
}

type semver struct {
	major, minor, patch int
	pre                 []string // nil when no pre-release
}

func parse(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.Index(s, "+"); i >= 0 {
		s = s[:i] // build metadata has no precedence
	}
	var pre []string
	if i := strings.Index(s, "-"); i >= 0 {
		pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	v := semver{pre: pre}
	for i := 0; i < 3; i++ {
		var n int
		if i < len(parts) {
			p := parts[i]
			if p == "" || strings.ContainsAny(p, "+-") {
				return semver{}, false
			}
			parsed, err := strconv.Atoi(p)
			if err != nil {
				return semver{}, false
			}
			n = parsed
		}
		switch i {
		case 0:
			v.major = n
		case 1:
			v.minor = n
		case 2:
			v.patch = n
		}
	}
	return v, true
}

func cmpInt(x, y int) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// comparePre implements the SemVer pre-release rule: a release without a
// pre-release has higher precedence than one with, and identifiers compare
// numerically (numbers) then ASCII-lexically (alphanumeric).
func comparePre(a, b []string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a), len(b))
}

func compareIdent(x, y string) int {
	xn, xerr := strconv.Atoi(x)
	yn, yerr := strconv.Atoi(y)
	switch {
	case xerr == nil && yerr == nil:
		return cmpInt(xn, yn)
	case xerr == nil:
		return -1 // numeric identifiers sort lower than alphanumeric
	case yerr == nil:
		return 1
	}
	return strings.Compare(x, y)
}
