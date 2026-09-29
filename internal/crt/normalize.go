package crt

import (
	"net/url"
	"strings"
)

// NormalizeApex turns user input into a bare registrable domain. It accepts a
// plain domain or a full URL and strips the scheme, userinfo, port,
// path/query/fragment, a leading "www." or "*." and any trailing dots.
//
//	https://ahadbuilders.com/   -> ahadbuilders.com
//	http://WWW.x.com:8443/a?b=1 -> x.com
func NormalizeApex(input string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return ""
	}

	// Drop the scheme.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}

	// Keep only the authority: cut at the first path/query/fragment delimiter.
	s = strings.SplitN(s, "/", 2)[0]
	s = strings.SplitN(s, "?", 2)[0]
	s = strings.SplitN(s, "#", 2)[0]

	// Drop a leading wildcard before parsing (net/url rejects '*' in hosts).
	s = strings.TrimPrefix(s, "*.")

	// Strip userinfo and port.
	if u, err := url.Parse("//" + s); err == nil && u.Host != "" {
		s = u.Hostname()
	}

	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimRight(s, ".")
	return s
}
