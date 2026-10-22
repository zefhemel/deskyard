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
		{[]string{"mysql", "-uroot", "-phunter2"}, "mysql -uroot -p•••"},
		{[]string{"mariadb", "-p", "x1"}, "mariadb -p •••"},
		{[]string{"/usr/bin/mysqldump", "-pabc", "db"}, "/usr/bin/mysqldump -p••• db"},
		{[]string{"curl", "-ualice:pw", "x"}, "curl -u••• x"},
		{[]string{"curl", "-u", "alice", "x"}, "curl -u alice x"},
		{[]string{"tool", "-pS3cretPass9"}, "tool -p•••"},
		{[]string{"sh", "-c", "mysql -p hunter2 db && curl -u a:b x"}, "sh -c 'mysql -p ••• db && curl -u ••• x'"},
		{[]string{"sh", "-c", "ls; mkdir -p a/b | mysql -phunter2"}, "sh -c 'ls; mkdir -p a/b | mysql -p•••'"},
		{[]string{"sh", "-c", "mkdir -p src && ssh -p 22 h"}, "sh -c 'mkdir -p src && ssh -p 22 h'"},
		// Round 3: shared secret words, anywhere in the name.
		{[]string{"gpg", "--passphrase", "s"}, "gpg --passphrase •••"},
		{[]string{"x", "--passphrase=s"}, "x --passphrase=•••"},
		{[]string{"x", "--token-value", "s"}, "x --token-value •••"},
		{[]string{"x", "--auth-token", "s"}, "x --auth-token •••"},
		{[]string{"sh", "-c", "x --passphrase s"}, "sh -c 'x --passphrase •••'"},
		{[]string{"env", "DB_PW=hunter2", "x"}, "env DB_PW=••• x"},
		{[]string{"env", "MYSQL_PWD=x", "mysql"}, "env MYSQL_PWD=••• mysql"},
		{[]string{"env", "PGPASSWORD=x", "psql"}, "env PGPASSWORD=••• psql"},
		{[]string{"x", "--keyboard", "us"}, "x --keyboard us"},
		{[]string{"x", "--pass-through", "y"}, "x --pass-through y"},
		{[]string{"x", "--passive"}, "x --passive"},
		{[]string{"env", "AUTHOR=bob", "PWD=/x", "KEYBOARD=us", "x"}, "env AUTHOR=bob PWD=/x KEYBOARD=us x"},
		// Newlines separate commands; the display form is still one line.
		{[]string{"sh", "-c", "cd /app\nmysql -p hunter2 db"}, "sh -c 'cd /app mysql -p ••• db'"},
		{[]string{"sh", "-c", "cd /app\r\nmysql -phunter2"}, "sh -c 'cd /app  mysql -p•••'"},
		{[]string{"sh", "-c", "a mysql -p hunter2"}, "sh -c 'a mysql -p •••'"},
		{[]string{"sh", "-c", "mysql \\\n -p hunter2"}, "sh -c 'mysql   -p •••'"},
		// Wrappers don't hide mysql.
		{[]string{"sudo", "mysql", "-p", "hunter2"}, "sudo mysql -p •••"},
		{[]string{"time", "mysql", "-phunter2", "db"}, "time mysql -p••• db"},
		{[]string{"env", "X=1", "mysql", "-p", "hunter2"}, "env X=1 mysql -p •••"},
		{[]string{"xargs", "mysql", "-p", "hunter2"}, "xargs mysql -p •••"},
		{[]string{"sh", "-c", "sudo mysql -p hunter2"}, "sh -c 'sudo mysql -p •••'"},
		{[]string{"sh", "-c", "MYSQL_PWD=x mysql -p hunter2"}, "sh -c 'MYSQL_PWD=••• mysql -p •••'"},
		{[]string{"sh", "-c", "(mysql -p hunter2 db)"}, "sh -c '(mysql -p ••• db)'"},
		{[]string{"sh", "-c", "$(mysql -p hunter2)"}, "sh -c '$(mysql -p •••'"},
		{[]string{"sh", "-c", "`mysql -p hunter2`"}, "sh -c '`mysql -p •••'"},
		{[]string{"sh", "-c", "sh -c 'mysql -p hunter2 db'"}, `sh -c 'sh -c '\''mysql -p ••• db'\'''`},
		// Quoted values with spaces.
		{[]string{"sh", "-c", "curl -u 'bob:s3 cr3t' x"}, "sh -c 'curl -u ••• x'"},
		{[]string{"sh", "-c", "mysql -p 'hunter 2' db"}, "sh -c 'mysql -p ••• db'"},
		{[]string{"sh", "-c", "mysql -p'a b' c"}, "sh -c 'mysql -p••• c'"},
		{[]string{"sh", "-c", `mysql -p"a b" c`}, "sh -c 'mysql -p••• c'"},
		// The ':' rule leaves times and field separators alone.
		{[]string{"date", "-u", "+%H:%M"}, "date -u +%H:%M"},
		{[]string{"sort", "-u", "-t:", "f"}, "sort -u -t: f"},
		{[]string{"sh", "-c", "date -u +%H:%M; sort -u -t: f"}, "sh -c 'date -u +%H:%M; sort -u -t: f'"},
		// Ordinary short flags stay readable.
		{[]string{"mkdir", "-p", "a/b"}, "mkdir -p a/b"},
		{[]string{"mkdir", "-pv", "a"}, "mkdir -pv a"},
		{[]string{"ssh", "-p", "22", "h"}, "ssh -p 22 h"},
		{[]string{"sort", "-u", "f"}, "sort -u f"},
		{[]string{"git", "add", "-p"}, "git add -p"},
		{[]string{"git", "commit", "-p"}, "git commit -p"},
		{[]string{"docker", "run", "-u", "1000", "x"}, "docker run -u 1000 x"},
		{[]string{"sh", "-c", "mysql -p hunter2 db"}, "sh -c 'mysql -p ••• db'"},
		{[]string{"sh", "-c", "mysql -phunter2 db"}, "sh -c 'mysql -p••• db'"},
		{[]string{"sh", "-c", "mysql -uroot -phunter2"}, "sh -c 'mysql -uroot -p•••'"},
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

// Everything after a typer's `type` word is hidden, however it is run, to
// the end of the display string (rulings 18, 19).
func TestDisplayCommandHidesTypedText(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		// The review's three.
		{[]string{"bash", "-lc", "deskyard type 'hunter2 correct horse' && deskyard key Return"}, "bash -lc 'deskyard type •••'"},
		{[]string{"deskyard", "type", "--", "pw", "-h"}, "deskyard type •••"},
		{[]string{"xdotool", "type", "hunter2"}, "xdotool type •••"},
		// The re-review's: substitutions and heredocs don't end it.
		{[]string{"sh", "-c", "xdotool type --file - <<EOF\nSECRET\nEOF"}, "sh -c 'xdotool type •••'"},
		{[]string{"sh", "-c", "deskyard type $(cat <<EOF\nSECRET\nEOF\n)"}, "sh -c 'deskyard type •••'"},
		{[]string{"sh", "-c", "deskyard type $(echo x) SECRET"}, "sh -c 'deskyard type •••'"},
		{[]string{"sh", "-c", "deskyard type $(echo a;b) SECRET"}, "sh -c 'deskyard type •••'"},
		{[]string{"sh", "-c", "deskyard type `echo` SECRET"}, "sh -c 'deskyard type •••'"},
		{[]string{"deskyard", "type", "$(echo", "x)", "SECRET"}, "deskyard type •••"},
		{[]string{"sh", "-c", "x=$(deskyard type SECRET)"}, "sh -c 'x=$(deskyard type •••'"},
		{[]string{"sh", "-c", "tee >(deskyard type SECRET)"}, "sh -c 'tee >(deskyard type •••'"},
		{[]string{"sh", "-c", "cat <(deskyard type SECRET)"}, "sh -c 'cat <(deskyard type •••'"},
		{[]string{"sh", "-c", `deskyard type $'SEC\'; RET'`}, "sh -c 'deskyard type •••'"},
		{[]string{"bash", "-c", `deskyard type $'SEC\'; RET'; ls`}, "bash -c 'deskyard type •••'"},
		// A shell string that types: the arguments after it are typed too.
		{[]string{"sh", "-c", "deskyard -d $(cat f) type SECRET"}, "sh -c 'deskyard -d $(cat f) type •••'"},
		{[]string{"sh", "-c", "(cd /; deskyard) type SECRET"}, "sh -c '(cd /; deskyard) type •••'"},
		{[]string{"sh", "-c", `deskyard type "$1"`, "_", "SECRET"}, `sh -c 'deskyard type •••' •••`},
		// Bare, and as the parser sees it.
		{[]string{"deskyard", "type", "hello", "world"}, "deskyard type •••"},
		{[]string{"/usr/local/bin/deskyard", "-d", "alice", "type", "pw"}, "/usr/local/bin/deskyard -d alice type •••"},
		{[]string{"deskyard", "type", "--", "-h"}, "deskyard type •••"},
		{[]string{"deskyard", "type", "-h"}, "deskyard type •••"},
		{[]string{"xdotool", "type", "--delay", "50", "pw"}, "xdotool type •••"},
		{[]string{"xdotool", "key", "ctrl+l", "type", "pw"}, "xdotool key ctrl+l type •••"},
		{[]string{"deskyard", "type"}, "deskyard type"},
		// Wrapped.
		{[]string{"sudo", "-u", "user", "deskyard", "type", "pw"}, "sudo -u user deskyard type •••"},
		{[]string{"env", "DISPLAY=:1", "xdotool", "type", "pw"}, "env DISPLAY=:1 xdotool type •••"},
		{[]string{"time", "deskyard", "type", "pw"}, "time deskyard type •••"},
		{[]string{"deskyard", "exec", "--", "deskyard", "type", "pw"}, "deskyard exec -- deskyard type •••"},
		// In shell strings.
		{[]string{"sh", "-c", "deskyard type pw; deskyard key Return"}, "sh -c 'deskyard type •••'"},
		{[]string{"sh", "-c", "deskyard type 'a; b' | cat"}, "sh -c 'deskyard type •••'"},
		{[]string{"sh", "-c", "(deskyard type pw)"}, "sh -c '(deskyard type •••'"},
		{[]string{"sh", "-c", "echo $(xdotool type pw) x"}, "sh -c 'echo $(xdotool type •••'"},
		{[]string{"sh", "-c", "sudo env A=1 time deskyard type pw\nls"}, "sh -c 'sudo env A=1 time deskyard type •••'"},
		{[]string{"sh", "-c", "deskyard type -- pw -h"}, "sh -c 'deskyard type •••'"},
		{[]string{"bash", "-lc", "sh -c 'xdotool type pw'"}, "bash -lc 'sh -c '\\''xdotool type •••'"},
		{[]string{"sh", "-c", "ls && xdotool key ctrl+a type pw key Return"}, "sh -c 'ls && xdotool key ctrl+a type •••'"},
		{[]string{"deskyard", "exec", "--", "sh", "-c", "deskyard type pw && deskyard key Return"}, "deskyard exec -- sh -c 'deskyard type •••'"},
		// Not typing: readable.
		{[]string{"deskyard", "key", "Return"}, "deskyard key Return"},
		{[]string{"sh", "-c", "type ls; file type"}, "sh -c 'type ls; file type'"},
		{[]string{"sh", "-c", "deskyard click 1 2; echo type x"}, "sh -c 'deskyard click 1 2; echo type x'"},
		{[]string{"sh", "-c", "deskyard click 1 2", "type", "x"}, "sh -c 'deskyard click 1 2' type x"},
	}
	for _, c := range cases {
		if got := DisplayCommand(c.argv); got != c.want {
			t.Errorf("DisplayCommand(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
}

// The inside path masks the display form again: a typer inside a quoted
// shell string is found too.
func TestMaskTextHidesTypedText(t *testing.T) {
	cases := map[string]string{
		`bash -lc 'deskyard type '\''hunter2 correct horse'\'' && deskyard key Return'`: "bash -lc 'deskyard type •••",
		"sh -c 'deskyard type pw; ls'":                      "sh -c 'deskyard type •••",
		"deskyard type •••":                                 "deskyard type •••",
		`sh -c "xdotool type 'p w'" && ls`:                  `sh -c "xdotool type •••`,
		"deskyard type pw\ndeskyard key Tab":                "deskyard type •••",
		"sh -c 'xdotool type --file - <<EOF\nSECRET\nEOF'":  "sh -c 'xdotool type •••",
		"deskyard type `echo` SECRET":                       "deskyard type •••",
		"deskyard type $(echo x) SECRET":                    "deskyard type •••",
		"sh -c 'deskyard type $(cat <<EOF\nSECRET\nEOF\n)'": "sh -c 'deskyard type •••",
		`deskyard type $'SEC\'; RET'`:                       "deskyard type •••",
	}
	for in, want := range cases {
		if got := MaskText(in); got != want {
			t.Errorf("MaskText(%q) = %q, want %q", in, got, want)
		}
	}
	// And through Sanitize, as an old CLI's unmasked display form.
	e, ok := Sanitize(Event{Agent: "a", Kind: "cmd-start", ID: "1", Cmd: "sh -c 'xdotool type --file - <<EOF\nSECRET\nEOF'"})
	if !ok || strings.Contains(e.Cmd, "SECRET") || strings.Contains(e.Cmd, "EOF") {
		t.Errorf("Sanitize: %q", e.Cmd)
	}
}
