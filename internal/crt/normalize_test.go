package crt

import "testing"

func TestNormalizeApex(t *testing.T) {
	cases := map[string]string{
		"https://ahadbuilders.com/":     "ahadbuilders.com",
		"http://ahadbuilders.com":       "ahadbuilders.com",
		"ahadbuilders.com/":             "ahadbuilders.com",
		"ahadbuilders.com":              "ahadbuilders.com",
		"  HTTPS://AhadBuilders.com// ": "ahadbuilders.com",
		"http://WWW.x.com:8443/a?b=1":   "x.com",
		"https://user:pass@x.com/p":     "x.com",
		"www.x.com":                     "x.com",
		"*.x.com":                       "x.com",
		"x.com.":                        "x.com",
		"":                              "",
	}
	for in, want := range cases {
		if got := NormalizeApex(in); got != want {
			t.Errorf("NormalizeApex(%q) = %q, want %q", in, got, want)
		}
	}
}
