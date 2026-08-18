package spaces

import (
	"fmt"
	"regexp"
	"strings"
)

const maxAgentName = 32

var agentNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidAgentName reports whether s is a legal Herdr agent name.
func ValidAgentName(s string) bool {
	return agentNameRe.MatchString(s)
}

// SanitizeAgentName turns raw into a legal Herdr agent name.
// fallback is used when raw sanitizes to empty (typically the agent kind).
func SanitizeAgentName(raw, fallback string) string {
	if s := sanitizeOnce(raw); s != "" {
		return s
	}
	if s := sanitizeOnce(fallback); s != "" {
		return s
	}
	return "agent"
}

func sanitizeOnce(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastHyphen := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if b.Len() > 0 && !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-_")
	if s == "" {
		return ""
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "a" + s
	}
	if len(s) > maxAgentName {
		s = strings.TrimRight(s[:maxAgentName], "-_")
		if s == "" || s[0] < 'a' || s[0] > 'z' {
			return ""
		}
	}
	return s
}

// UniqueAgentName returns want, or want-2 / want-3 / ... if want is taken.
func UniqueAgentName(want string, taken []string) string {
	set := make(map[string]struct{}, len(taken))
	for _, t := range taken {
		if t != "" {
			set[t] = struct{}{}
		}
	}
	if want == "" {
		want = "agent"
	}
	if _, used := set[want]; !used {
		return want
	}
	for i := 2; i < 1000; i++ {
		suffix := fmt.Sprintf("-%d", i)
		keep := maxAgentName - len(suffix)
		if keep < 1 {
			keep = 1
		}
		base := want
		if len(base) > keep {
			base = strings.TrimRight(base[:keep], "-_")
		}
		if base == "" || base[0] < 'a' || base[0] > 'z' {
			base = "a"
		}
		candidate := base + suffix
		if _, used := set[candidate]; !used {
			return candidate
		}
	}
	return want + "-x"
}
