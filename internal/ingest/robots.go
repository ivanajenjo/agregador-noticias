package ingest

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type robotsRule struct {
	allow bool
	path  string
}

type robotsData struct {
	rules   []robotsRule
	fetched time.Time
}

// robotsCache fetches and caches robots.txt per host (fail-open on errors).
type robotsCache struct {
	mu    sync.Mutex
	data  map[string]*robotsData
	ttl   time.Duration
	agent string // lowercase product token
	get   func(ctx context.Context, u string) (*http.Response, error)
}

func newRobotsCache(userAgent string, get func(ctx context.Context, u string) (*http.Response, error)) *robotsCache {
	tok := strings.ToLower(userAgent)
	if i := strings.IndexAny(tok, "/ "); i > 0 {
		tok = tok[:i]
	}
	return &robotsCache{data: map[string]*robotsData{}, ttl: 6 * time.Hour, agent: tok, get: get}
}

// Allowed reports whether fetching u is permitted.
func (c *robotsCache) Allowed(ctx context.Context, u *url.URL) bool {
	key := u.Scheme + "://" + u.Host
	c.mu.Lock()
	d := c.data[key]
	c.mu.Unlock()
	if d == nil || time.Since(d.fetched) > c.ttl {
		d = c.load(ctx, key)
		c.mu.Lock()
		c.data[key] = d
		c.mu.Unlock()
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return d.allowed(p)
}

func (c *robotsCache) load(ctx context.Context, origin string) *robotsData {
	d := &robotsData{fetched: time.Now()}
	resp, err := c.get(ctx, origin+"/robots.txt")
	if err != nil {
		return d
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return d
	}
	d.rules = parseRobots(io.LimitReader(resp.Body, 512<<10), c.agent)
	return d
}

// parseRobots returns the rules of the group that best matches agent
// (specific group wins over "*").
func parseRobots(r io.Reader, agent string) []robotsRule {
	var specific, star []robotsRule
	var cur []string
	inRules := false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if inRules {
				cur, inRules = nil, false
			}
			cur = append(cur, strings.ToLower(v))
		case "allow", "disallow":
			inRules = true
			if v == "" {
				continue // empty Disallow = allow all; empty Allow = no-op
			}
			rule := robotsRule{allow: k == "allow", path: v}
			for _, a := range cur {
				if a == "*" {
					star = append(star, rule)
				} else if a != "" && strings.Contains(agent, a) {
					specific = append(specific, rule)
				}
			}
		}
	}
	if len(specific) > 0 {
		return specific
	}
	return star
}

func (d *robotsData) allowed(path string) bool {
	best, bestLen := true, -1
	for _, r := range d.rules {
		if l, ok := robotsMatch(r.path, path); ok {
			if l > bestLen || (l == bestLen && r.allow) {
				best, bestLen = r.allow, l
			}
		}
	}
	return best
}

// robotsMatch matches a robots pattern (supports * and trailing $) as a prefix
// of path and returns the pattern length as specificity.
func robotsMatch(pat, path string) (int, bool) {
	anchored := strings.HasSuffix(pat, "$")
	pat = strings.TrimSuffix(pat, "$")
	parts := strings.Split(pat, "*")
	pos := 0
	for i, part := range parts {
		if i == 0 {
			if !strings.HasPrefix(path, part) {
				return 0, false
			}
			pos = len(part)
			continue
		}
		j := strings.Index(path[pos:], part)
		if j < 0 {
			return 0, false
		}
		pos += j + len(part)
	}
	if anchored && pos != len(path) && !strings.HasSuffix(pat, "*") {
		// last literal part must end the path
		last := parts[len(parts)-1]
		if !strings.HasSuffix(path, last) {
			return 0, false
		}
	}
	return len(pat), true
}
