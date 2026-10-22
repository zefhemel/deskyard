package activity

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDisplayCommand(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"deploy", "--token", "abc"}, "deploy --token •••"},
		{[]string{"deploy", "--token=abc"}, "deploy --token=•••"},
		{[]string{"mysql", "-p", "hunter2", "db"}, "mysql -p ••• db"},
		{[]string{"env", "GITHUB_TOKEN=x1", "API_KEY=y", "PATH=/bin", "make"}, "env GITHUB_TOKEN=••• API_KEY=••• PATH=/bin make"},
		{[]string{"curl", "-H", "Authorization: Bearer dyt_ab12_cd34"}, "curl -H 'Authorization: Bearer •••'"},
		{[]string{"sh", "-c", "OPENAI_KEY=sk-proj-abc123 ./run --secret s"}, "sh -c 'OPENAI_KEY=••• ./run --secret •••'"},
		{[]string{"git", "clone", "https://ghp_aaaaaaaaaaaaaaaaaaaa@github.com/x"}, "git clone https://•••@github.com/x"},
		{[]string{"echo", strings.Repeat("ab12", 10)}, "echo •••"},
		{[]string{"echo", "hello world"}, "echo 'hello world'"},
		{[]string{"ls", "-la"}, "ls -la"},
		// More: hide rather than show.
		{[]string{"x", "--api-key", "zzz"}, "x --api-key •••"},
		{[]string{"x", "github_pat_11ABC_def"}, "x •••"},
		{[]string{"x", "it's"}, `x 'it'\''s'`},
		{[]string{"x", ""}, "x ''"},
		{[]string{"cat", "/workspace/projects/app-2/src/components/Foo.tsx"}, "cat /workspace/projects/app-2/src/components/Foo.tsx"},
		{[]string{"x", "--token"}, "x --token"},
	}
	for _, c := range cases {
		if got := DisplayCommand(c.argv); got != c.want {
			t.Errorf("DisplayCommand(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
}

func TestDisplayCommandCut(t *testing.T) {
	got := DisplayCommand([]string{"echo", strings.Repeat("a b ", 100)})
	if n := utf8.RuneCountInString(got); n != 121 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %d runes, %q", n, got)
	}
}

func TestMaskText(t *testing.T) {
	cases := map[string]string{
		"curl --password hunter2 http://x": "curl --password ••• http://x",
		"DB_PASS=abc run":                  "DB_PASS=••• run",
		"token dyt_AbC_123":                "token •••",
		"nothing to see":                   "nothing to see",
		"a  b":                             "a  b",
	}
	for in, want := range cases {
		if got := MaskText(in); got != want {
			t.Errorf("MaskText(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzMaskText(f *testing.F) {
	for _, s := range []string{"", "--token abc", "A_KEY=1", "dyt_x", strings.Repeat("a1", 40)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := MaskText(s)
		if MaskText(out) != out {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, out, MaskText(out))
		}
		DisplayCommand([]string{s, s})
	})
}
