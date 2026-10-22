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
		// Review round 1.
		{[]string{"curl", "-u", "alice:hunter2", "http://x"}, "curl -u ••• http://x"},
		{[]string{"curl", "--user", "alice:hunter2", "http://x"}, "curl --user ••• http://x"},
		{[]string{"curl", "--user=alice:hunter2"}, "curl --user=•••"},
		{[]string{"mysql", "-phunter2", "db"}, "mysql -p••• db"},
		{[]string{"mysql", "-uroot", "-phunter2"}, "mysql -u••• -p•••"},
		{[]string{"sh", "-c", "mysql -p hunter2 db"}, "sh -c 'mysql -p ••• db'"},
		{[]string{"sh", "-c", "mysql -phunter2 db"}, "sh -c 'mysql -p••• db'"},
		{[]string{"sh", "-c", "curl -u bob:s3cr3t https://x"}, "sh -c 'curl -u ••• https://x'"},
		{[]string{"sh", "-c", "curl --user bob:s3cr3t https://x"}, "sh -c 'curl --user ••• https://x'"},
		{[]string{"x", "--pass", "hunter2"}, "x --pass •••"},
		{[]string{"x", "--pw", "hunter2"}, "x --pw •••"},
		{[]string{"x", "--auth", "hunter2"}, "x --auth •••"},
		{[]string{"x", "--credentials", "hunter2"}, "x --credentials •••"},
		{[]string{"x", "--pw=hunter2"}, "x --pw=•••"},
		{[]string{"x", "--author", "bob"}, "x --author bob"},
		{[]string{"aws", "configure", "set", "aws_secret_access_key", "wJalrXUtnFEMI"}, "aws configure set aws_secret_access_key •••"},
		{[]string{"sh", "-c", "echo aws_secret_access_key=abc"}, "sh -c 'echo aws_secret_access_key=•••'"},
		{[]string{"echo", "AKIAIOSFODNN7EXAMPLE"}, "echo •••"},
		{[]string{"echo", "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\ndef\n-----END RSA PRIVATE KEY----- after"}, "echo '••• after'"},
		{[]string{"echo", "-----BEGIN PRIVATE KEY-----\nMIIabc cut short"}, "echo •••"},
		// Control and format characters never reach logs or the UI.
		{[]string{"x", "\x1b[31mred"}, "x ' [31mred'"},
		{[]string{"x", "a\nb"}, "x 'a b'"},
		{[]string{"x", "a\u202eb"}, "x ab"},
		{[]string{"x", "dyt_ab\u200bcd"}, "x •••"},
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

func TestMaskTextPEMAndAWS(t *testing.T) {
	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaA\n-----END OPENSSH PRIVATE KEY-----"
	if got := MaskText("a " + pem + " b"); got != "a ••• b" {
		t.Errorf("pem: %q", got)
	}
	if got := MaskText("aws_secret_access_key abc/def+ghi"); got != "aws_secret_access_key •••" {
		t.Errorf("aws: %q", got)
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
