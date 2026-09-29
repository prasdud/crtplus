package bucketrecon

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

var builtinWords = []string{
	"dev", "development", "staging", "stage", "stg", "prod", "production", "test", "testing",
	"qa", "uat", "sandbox", "demo", "beta", "alpha", "preview", "internal", "intranet",
	"assets", "asset", "static", "statics", "media", "images", "image", "img", "imgs",
	"video", "videos", "docs", "doc", "files", "file", "uploads", "upload", "downloads",
	"download", "backup", "backups", "bak", "data", "dataset", "datasets", "logs", "log",
	"public", "private", "cdn", "web", "www", "app", "api", "storage", "store", "content",
	"resources", "archive", "tmp", "temp", "cache", "reports", "export", "exports",
	"static-assets", "user-assets", "app-assets", "web-assets", "site-assets", "staticfiles",
	"attachments", "images-prod", "media-assets", "user-uploads", "bucket", "s3",
}

var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var ipRe = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

// Candidates generates S3 bucket-name guesses from a domain and optional extra
// words. Rules mirror S3 naming (lowercase, 3-63 chars, no underscores).
func Candidates(domain string, extraWords []string) []string {
	name := registrableName(domain)
	words := append(append([]string{}, builtinWords...), extraWords...)

	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if b := normalizeBucket(s); b != "" && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}

	dashed := strings.ReplaceAll(strings.ToLower(domain), ".", "-")
	add(name)
	add(strings.ToLower(domain))
	add(dashed)
	add(strings.ReplaceAll(strings.ToLower(domain), ".", ""))

	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		add(name + "-" + w)
		add(w + "-" + name)
		add(name + w)
		add(w + name)
		add(name + "." + w)
		add(w + "." + name)
		add(w)
	}
	return out
}

func registrableName(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, "www.")
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return parts[0]
	}
	secondLevel := map[string]bool{
		"co": true, "com": true, "org": true, "net": true, "gov": true,
		"ac": true, "edu": true, "gen": true, "ind": true,
	}
	if len(parts) >= 3 && secondLevel[parts[len(parts)-2]] {
		return parts[len(parts)-3]
	}
	return parts[len(parts)-2]
}

func normalizeBucket(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.Trim(s, ".-")
	if ipRe.MatchString(s) {
		return ""
	}
	if strings.Contains(s, "..") || strings.Contains(s, ".-") || strings.Contains(s, "-.") {
		return ""
	}
	if !bucketNameRe.MatchString(s) {
		return ""
	}
	return s
}

// LoadWords reads extra mutation words from a file, one per line.
func LoadWords(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var words []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		w := strings.TrimSpace(sc.Text())
		if w != "" && !strings.HasPrefix(w, "#") {
			words = append(words, w)
		}
	}
	return words, sc.Err()
}
