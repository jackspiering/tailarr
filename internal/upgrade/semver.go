package upgrade

import (
	"strconv"
	"strings"
)

// Comparable reports whether both strings parse as SemVer (optional leading v).
func Comparable(a, b string) bool {
	_, aok := parse(a)
	_, bok := parse(b)
	return aok && bok
}

// Compare compares two SemVer strings using SemVer 2.0.0 precedence rules.
// A leading "v" (or "V") is optional and ignored, as is build metadata.
// Returns -1 when a < b, 0 when equal, +1 when a > b.
// Non-SemVer inputs compare equal (0); use Comparable to detect that case.
func Compare(a, b string) int {
	pa, aok := parse(a)
	pb, bok := parse(b)
	if !aok || !bok {
		// Do not invent order for dirty build tags; callers should use
		// Comparable and confirm before upgrading.
		return 0
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
	if s == "" {
		return semver{}, false
	}
	if i := strings.Index(s, "+"); i >= 0 {
		if !validBuild(s[i+1:]) {
			return semver{}, false
		}
		s = s[:i]
	}
	var pre []string
	if i := strings.Index(s, "-"); i >= 0 {
		raw := s[i+1:]
		s = s[:i]
		if raw == "" {
			return semver{}, false
		}
		pre = strings.Split(raw, ".")
		for _, id := range pre {
			if !validPreIdent(id) {
				return semver{}, false
			}
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	v := semver{pre: pre}
	nums := [3]*int{&v.major, &v.minor, &v.patch}
	for i, p := range parts {
		if !validNumeric(p) {
			return semver{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, false
		}
		*nums[i] = n
	}
	return v, true
}

func validNumeric(p string) bool {
	if p == "" || (len(p) > 1 && p[0] == '0') {
		return false
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func validPreIdent(id string) bool {
	if id == "" {
		return false
	}
	numeric := true
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '-':
			numeric = false
		default:
			return false
		}
	}
	if numeric && len(id) > 1 && id[0] == '0' {
		return false
	}
	return true
}

func validBuild(b string) bool {
	if b == "" {
		return false
	}
	for _, id := range strings.Split(b, ".") {
		if id == "" {
			return false
		}
		for _, c := range id {
			if (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && c != '-' {
				return false
			}
		}
	}
	return true
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
