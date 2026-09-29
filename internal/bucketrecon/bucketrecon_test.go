package bucketrecon

import "testing"

func TestExtractBuckets(t *testing.T) {
	in := []byte(`
		https://my-bucket.s3.us-west-2.amazonaws.com/a.png
		"https://cdn.example.com/x"
		https://s3.amazonaws.com/path-bucket/index.html
		https://pub-abc123.r2.dev/logo.png
		https://acct123.r2.cloudflarestorage.com/mybucket/file.txt
		http://ads.india.com.s3-ap-south-1.amazonaws.com/2018-logo.png
	`)
	refs := ExtractBuckets(in, SourceJS)

	got := map[string]bool{}
	for _, r := range refs {
		got[string(r.Provider)+":"+r.Name] = true
	}

	want := []string{
		"s3:my-bucket",
		"s3:path-bucket",
		"s3:ads.india.com",
		"r2:pub-abc123.r2.dev",
		"r2:mybucket",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing expected ref %q; got %v", w, got)
		}
	}
}

func TestCandidates(t *testing.T) {
	got := Candidates("example.com", []string{"custom"})
	set := map[string]bool{}
	for _, c := range got {
		set[c] = true
		if c != normalizeBucket(c) {
			t.Errorf("candidate %q is not normalized", c)
		}
	}

	for _, want := range []string{"example", "example.com", "example-com", "exampleassets", "assets-example", "example-custom", "custom"} {
		if !set[want] {
			t.Errorf("missing candidate %q", want)
		}
	}
}

func TestNormalizeBucket(t *testing.T) {
	cases := map[string]string{
		"Example_Bucket": "example-bucket",
		"my-bucket":      "my-bucket",
		"My.Bucket":      "my.bucket",
		"1.2.3.4":        "",
		"ab":             "",
		"-leading":       "leading",
	}
	for in, want := range cases {
		if got := normalizeBucket(in); got != want {
			t.Errorf("normalizeBucket(%q) = %q, want %q", in, got, want)
		}
	}
}
