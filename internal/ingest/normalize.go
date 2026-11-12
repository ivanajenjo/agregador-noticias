package ingest

import (
	"errors"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// trackingParams are query parameters dropped when canonicalizing URLs.
var trackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "gbraid": true, "wbraid": true,
	"msclkid": true, "yclid": true, "mc_cid": true, "mc_eid": true, "igshid": true,
	"ocid": true, "cmpid": true, "xtor": true, "ito": true, "s_kwcid": true,
	"_ga": true, "_gl": true, "ref_src": true, "mkt_tok": true, "wt_mc": true,
	"at_medium": true, "at_campaign": true, "at_custom1": true, "at_custom2": true,
	"at_custom3": true, "at_custom4": true, "outputType": true, "ns_source": true,
	"ns_mchannel": true, "ns_campaign": true, "ns_linkname": true, "ns_fee": true,
	"intcmp": true, "int_cmp": true, "rss": true, "ssm": true, "sref": true,
}

// CanonicalURL normalizes raw (optionally relative to base) into a stable key
// used for deduplication: https scheme, lowercase host without default port,
// no fragment, no tracking parameters, sorted query, no trailing slash.
func CanonicalURL(raw, base string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if base != "" {
		if b, err := url.Parse(base); err == nil {
			u = b.ResolveReference(u)
		}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("unsupported scheme: " + u.Scheme)
	}
	if u.Hostname() == "" {
		return "", errors.New("missing host")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "80" || port == "443" {
		port = ""
	}
	if strings.Contains(host, ":") { // IPv6 literal
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "utm_") || trackingParams[lk] {
			q.Del(k)
		}
	}
	out := url.URL{Scheme: "https", Host: host, Path: u.EscapedPath()}
	if p, err := url.PathUnescape(out.Path); err == nil {
		out.Path = p
		out.RawPath = u.EscapedPath()
	}
	if len(out.Path) > 1 {
		out.Path = strings.TrimRight(out.Path, "/")
		out.RawPath = strings.TrimRight(out.RawPath, "/")
	} else {
		out.Path, out.RawPath = "", ""
	}
	if len(q) > 0 {
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out.RawQuery = q.Encode()
	}
	return out.String(), nil
}

var blockTags = map[string]bool{
	"p": true, "br": true, "div": true, "li": true, "ul": true, "ol": true, "h1": true,
	"h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "tr": true,
	"blockquote": true, "section": true, "article": true, "figure": true, "figcaption": true,
}

// StripHTML converts an HTML fragment to plain text with collapsed whitespace.
func StripHTML(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return collapseSpace(s)
	}
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return collapseSpace(b.String())
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			n := string(name)
			if n == "script" || n == "style" {
				if z.Token().Type == html.StartTagToken {
					skip++
				}
			} else if blockTags[n] {
				b.WriteByte(' ')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			n := string(name)
			if (n == "script" || n == "style") && skip > 0 {
				skip--
			} else if blockTags[n] {
				b.WriteByte(' ')
			}
		}
	}
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")
}

// truncateRunes cuts s to at most max runes (on a rune boundary).
func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}
