package browser

import (
	"net/url"
	"strings"
)

// robotsRule is one Allow or Disallow line of a robots.txt, with the path
// pattern exactly as written (it may contain * and a trailing $).
type robotsRule struct {
	allow   bool
	pattern string
}

// robotsRules is a parsed robots.txt: one group of rules per user-agent token,
// plus the "*" group that matches anything.
type robotsRules struct {
	groups map[string][]robotsRule
}

// robotsAllowed reports whether the URL may be fetched under the origin's
// robots.txt. It is only reached when Options.ObeyRobots is set, so the fast
// path never pays for it.
func (b *Browser) robotsAllowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return true
	}
	origin := u.Scheme + "://" + u.Host
	rules := b.robotsFor(origin)
	if rules == nil {
		return true
	}

	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}

	group := rules.groupFor(b.robotsUserAgent())
	bestLen := -1
	bestAllow := true
	for _, r := range group {
		n, ok := robotsMatch(r.pattern, path)
		if !ok {
			continue
		}
		switch {
		case n > bestLen:
			bestLen, bestAllow = n, r.allow
		case n == bestLen && r.allow:
			// On an equally long match, Allow wins, as in the spec.
			bestAllow = true
		}
	}
	return bestLen < 0 || bestAllow
}

// robotsUserAgent returns the token robots.txt groups are matched against. It
// is the configured User-Agent, or the impersonation target's if that is
// empty, lowercased. An empty result matches only the "*" group.
func (b *Browser) robotsUserAgent() string {
	ua := b.opts.UserAgent
	if ua == "" {
		ua = b.sess.UserAgent()
	}
	return strings.ToLower(ua)
}

// groupFor returns the rules whose user-agent token best matches ua: the
// longest token that appears in the UA wins, and "*" is the fallback.
func (r *robotsRules) groupFor(ua string) []robotsRule {
	best := ""
	for token := range r.groups {
		if token == "*" {
			continue
		}
		if strings.Contains(ua, token) && len(token) > len(best) {
			best = token
		}
	}
	if best != "" {
		return r.groups[best]
	}
	return r.groups["*"]
}

// robotsMatch reports whether path matches a robots.txt pattern, and how long
// the pattern is (for the longest-match precedence rule). A pattern matches a
// prefix; "*" matches any run of characters and a trailing "$" anchors the end.
func robotsMatch(pattern, path string) (int, bool) {
	if pattern == "" {
		return -1, false
	}
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = pattern[:len(pattern)-1]
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		// No wildcard: a plain prefix match.
		if !strings.HasPrefix(path, pattern) {
			return -1, false
		}
		if anchored && path != pattern {
			return -1, false
		}
		return len(pattern), true
	}
	// Walk the parts in order; the first must be a prefix and the last a suffix
	// when anchored.
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i == 0 {
			if !strings.HasPrefix(path[pos:], part) {
				return -1, false
			}
			pos += len(part)
			continue
		}
		idx := strings.Index(path[pos:], part)
		if idx < 0 {
			return -1, false
		}
		pos += idx + len(part)
	}
	last := parts[len(parts)-1]
	if anchored && last != "" && !strings.HasSuffix(path, last) {
		return -1, false
	}
	return len(pattern), true
}

// robotsFor returns the parsed rules for an origin, fetching and caching
// robots.txt once per origin. It returns nil when the file is absent or
// unreadable, which means "allow everything".
func (b *Browser) robotsFor(origin string) *robotsRules {
	b.robotsMu.Lock()
	defer b.robotsMu.Unlock()
	if r, ok := b.robots[origin]; ok {
		return r
	}
	var rules *robotsRules
	resp, err := b.sess.Get(origin + "/robots.txt")
	if err == nil && resp.StatusCode < 400 {
		rules = parseRobots(string(resp.Content))
	}
	b.robots[origin] = rules
	return rules
}

// parseRobots parses the subset of robots.txt that matters for fetching: user-
// agent groups with allow and disallow rules. Unknown directives are ignored.
func parseRobots(body string) *robotsRules {
	r := &robotsRules{groups: map[string][]robotsRule{}}
	var agents []string
	seenRules := false
	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "user-agent":
			// A new user-agent line after rules starts a fresh group.
			if seenRules {
				agents = nil
				seenRules = false
			}
			agents = append(agents, strings.ToLower(value))
		case "allow", "disallow":
			if len(agents) == 0 {
				continue
			}
			seenRules = true
			// An empty Disallow means "allow everything", which is the default.
			if key == "disallow" && value == "" {
				continue
			}
			rule := robotsRule{allow: key == "allow", pattern: value}
			for _, a := range agents {
				r.groups[a] = append(r.groups[a], rule)
			}
		}
	}
	return r
}
