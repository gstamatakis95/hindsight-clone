package mentalmodel

import (
	"strconv"
	"strings"
	"testing"
)

const diffHeader = "--- current\n+++ proposed\n"

func TestDiffMarkdown(t *testing.T) {
	cases := []struct {
		name, before, after, want string
	}{
		{"identical", "a\nb\n", "a\nb\n", ""},
		{"both empty", "", "", ""},
		{"trailing newline only", "a\nb", "a\nb\n", ""},
		{
			"simple change", "1\n2\n3\n4\n5\n6\n7\n8\n", "1\n2\n3\n4\nFIVE\n6\n7\n8\n",
			diffHeader + "@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+FIVE\n 6\n 7\n 8\n",
		},
		{
			"insert at start", "a\nb\n", "new\na\nb\n",
			diffHeader + "@@ -1,2 +1,3 @@\n+new\n a\n b\n",
		},
		{
			"insert at end", "a\nb\n", "a\nb\nnew\n",
			diffHeader + "@@ -1,2 +1,3 @@\n a\n b\n+new\n",
		},
		{
			"from empty", "", "x\ny\n",
			diffHeader + "@@ -0,0 +1,2 @@\n+x\n+y\n",
		},
		{
			"to empty", "x\n", "",
			diffHeader + "@@ -1,1 +0,0 @@\n-x\n",
		},
		{
			"two hunks", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n", "X\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\nY\n",
			diffHeader + "@@ -1,4 +1,4 @@\n-1\n+X\n 2\n 3\n 4\n@@ -9,4 +9,4 @@\n 9\n 10\n 11\n-12\n+Y\n",
		},
		{
			"close changes merge", "1\n2\n3\n4\n5\n6\n7\n8\n", "X\n2\n3\n4\n5\n6\n7\nY\n",
			diffHeader + "@@ -1,8 +1,8 @@\n-1\n+X\n 2\n 3\n 4\n 5\n 6\n 7\n-8\n+Y\n",
		},
		{
			"interleaved lcs", "a\nb\nc\nd\n", "a\nc\nd\ne\n",
			diffHeader + "@@ -1,4 +1,4 @@\n a\n-b\n c\n d\n+e\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := diffMarkdown(c.before, c.after); got != c.want {
				t.Fatalf("diffMarkdown =\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

func TestDiffMarkdownLargeFallsBackToReplace(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i <= maxDiffLines; i++ {
		a.WriteString("a\n")
		b.WriteString("b\n")
	}
	got := diffMarkdown("keep\n"+a.String(), "keep\n"+b.String())
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	n := maxDiffLines + 1
	if lines[2] != "@@ -1,"+strconv.Itoa(n+1)+" +1,"+strconv.Itoa(n+1)+" @@" || lines[3] != " keep" {
		t.Fatalf("unexpected header: %q %q", lines[2], lines[3])
	}
	if lines[4] != "-a" || lines[4+n] != "+b" || len(lines) != 4+2*n {
		t.Fatalf("fallback is not a whole replace: %d lines", len(lines))
	}
}
