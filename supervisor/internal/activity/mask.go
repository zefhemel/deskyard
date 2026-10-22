package activity

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Every masking pattern lives in this file. The rule is to hide when in
// doubt: a false positive costs a few characters of a display string, a miss
// leaks a secret into a history other people can read.

const masked = "•••"

// maxDisplay is where a command's display form is cut.
const maxDisplay = 120

// maxArg bounds the work done on one argument; anything past it is beyond
// the cut anyway.
const maxArg = 4096

// secretWords are the name parts that mark a flag or variable as secret,
// wherever they sit in the name (--passphrase, --token-value, DB_PW,
// MYSQL_PWD). Names with one of the notSecret parts are left readable.
var (
	secretWords = []string{"pass", "pw", "token", "secret", "key", "auth", "credential"}
	notSecret   = []string{"author", "keyboard", "passive", "pass-through", "passthrough", "authority"}
)

// secretName reports whether a flag or variable name looks secret.
func secretName(name string) bool {
	n := strings.ToLower(name)
	if n == "pwd" || n == "--pwd" { // the shell's working directory
		return false
	}
	for _, w := range notSecret {
		n = strings.ReplaceAll(n, w, "")
	}
	return slices.ContainsFunc(secretWords, func(w string) bool { return strings.Contains(n, w) })
}

// A value: quoted (to the end if unterminated) or one word.
const valueRE = `(?:"[^"]*"?|'[^']*'?|\S+)`

var (
	// A flag whose value is the next argument: --token, --api-key, ...
	longFlag = regexp.MustCompile(`^--[A-Za-z0-9-]+$`)

	// Short flags carry secrets only in some commands, so they are masked
	// by context (see maskShort): -u/--user VALUE when VALUE is user:pass
	// (curl), -p VALUE in mysql and friends, attached -pVALUE when a mysql
	// command is involved or VALUE looks like a secret.
	userFlag  = regexp.MustCompile(`(^|[\s'"])(-u\s*|--user(?:=|\s+))(` + valueRE + `)`)
	pwdAny    = regexp.MustCompile(`(^|[\s'"])(-p\s*)` + valueRE)
	pwdAttach = regexp.MustCompile(`(^|[\s'"])(-p)(\S{6,})`)
	// Where one simple command ends and the next begins.
	lineCont = regexp.MustCompile(`\\\n+`)
	cmdSep   = regexp.MustCompile(`&&|\|\||[;|&\n]`)

	// A long flag with its value in the same string: --token=abc, --secret abc.
	flagValue = regexp.MustCompile(`(--[A-Za-z0-9-]+)(=|\s+)(` + valueRE + `)`)

	// PEM private keys, whole (or to the end of the string when cut short),
	// and AWS credentials.
	pem       = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	awsSecret = regexp.MustCompile(`(?i)(aws_secret_access_key(?:\s*[=:]\s*|\s+))(?:"[^"]*"?|'[^']*'?|\S+)`)
	awsKeyID  = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)

	// KEY=value where the name looks secret; the value may be quoted.
	assignment = regexp.MustCompile(`\b([A-Za-z0-9_]+)=(?:"[^"]*"?|'[^']*'?|[^\s'"]+)`)

	// Token-shaped strings.
	dyt    = regexp.MustCompile(`dyt_[A-Za-z0-9_]+`)
	sk     = regexp.MustCompile(`(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]+`)
	gh     = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]+`)
	ghPat  = regexp.MustCompile(`github_pat_[A-Za-z0-9_]+`)
	bearer = regexp.MustCompile(`(?i)\b(bearer\s+)\S+`)
	// Authorization: Scheme credentials
	authHeader = regexp.MustCompile(`(?i)(authorization:\s*\w+\s+)\S+`)
	// https://user:password@host
	userinfo = regexp.MustCompile(`(://)[^/\s:@]*:[^/\s@]+@`)

	// Long runs: hex, or base64/url-safe text mixing letters and digits.
	longRun = regexp.MustCompile(`[A-Za-z0-9_+=-]{32,}`)
	hexRun  = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// MaskText hides secrets in free text (a command line, a script in sh -c).
func MaskText(s string) string { return maskText(s, false) }

// mysqlLike are the commands whose -p takes the password.
var mysqlLike = []string{"mysql", "mariadb", "mysqldump"}

// hasMysql reports whether any word is a mysql-like command, however it is
// wrapped: sudo mysql, env X=1 mysql, (mysql, $(mysql, `mysql, 'mysql.
func hasMysql(words []string) bool {
	return slices.ContainsFunc(words, func(w string) bool {
		w = strings.Trim(strings.TrimPrefix(w, "$"), "'\"()`{}")
		return slices.Contains(mysqlLike, path.Base(w))
	})
}

// maskText masks s; withMysql says s is part of a command line that runs a
// mysql-like command (an argument of its argv).
func maskText(s string, withMysql bool) string {
	s = lineCont.ReplaceAllString(s, " ") // a backslash-newline continues the command
	s = maskTyped(s)
	var b strings.Builder
	last := 0
	for _, loc := range append(cmdSep.FindAllStringIndex(s, -1), []int{len(s), len(s)}) {
		b.WriteString(maskShort(s[last:loc[0]], withMysql))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	return maskRest(b.String())
}

// unquote strips one pair of surrounding quotes, even an unterminated one.
func unquote(v string) string {
	if v != "" && (v[0] == '\'' || v[0] == '"') {
		v = strings.TrimSuffix(v[1:], v[:1])
	}
	return v
}

// maskShort masks the short-flag secrets of one simple command.
func maskShort(seg string, withMysql bool) string {
	seg = userFlag.ReplaceAllStringFunc(seg, func(m string) string {
		g := userFlag.FindStringSubmatch(m)
		if userValue(unquote(g[3])) {
			return g[1] + g[2] + masked
		}
		return m
	})
	if withMysql || hasMysql(strings.Fields(seg)) {
		return pwdAny.ReplaceAllString(seg, "${1}${2}"+masked)
	}
	return pwdAttach.ReplaceAllStringFunc(seg, func(m string) string {
		g := pwdAttach.FindStringSubmatch(m)
		if strings.ContainsAny(g[3], "0123456789") && containsLetter(g[3]) {
			return g[1] + g[2] + masked
		}
		return m
	})
}

// userValue is a -u/--user value that carries a password: user:pass, but not
// a flag or a format (date -u +%H:%M, sort -u -t: f).
func userValue(v string) bool {
	return strings.Contains(v, ":") && !strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "+")
}

func maskRest(s string) string {
	s = pem.ReplaceAllString(s, masked)
	s = awsSecret.ReplaceAllString(s, "${1}"+masked)
	s = awsKeyID.ReplaceAllString(s, masked)
	s = flagValue.ReplaceAllStringFunc(s, func(m string) string {
		g := flagValue.FindStringSubmatch(m)
		if secretName(g[1]) {
			return g[1] + g[2] + masked
		}
		return m
	})
	s = dyt.ReplaceAllString(s, masked)
	s = sk.ReplaceAllString(s, "${1}"+masked)
	s = gh.ReplaceAllString(s, masked)
	s = ghPat.ReplaceAllString(s, masked)
	s = assignment.ReplaceAllStringFunc(s, func(m string) string {
		name, _, _ := strings.Cut(m, "=")
		if secretName(name) {
			return name + "=" + masked
		}
		return m
	})
	s = authHeader.ReplaceAllString(s, "${1}"+masked)
	s = bearer.ReplaceAllString(s, "${1}"+masked)
	s = userinfo.ReplaceAllString(s, "${1}"+masked+"@")
	return longRun.ReplaceAllStringFunc(s, func(run string) string {
		if hexRun.MatchString(run) || (strings.ContainsAny(run, "0123456789") && containsLetter(run)) {
			return masked
		}
		return run
	})
}

func containsLetter(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i] | 0x20; c >= 'a' && c <= 'z' {
			return true
		}
	}
	return false
}

// Typed text. `deskyard type …` and `xdotool type …` type whatever follows,
// a password as easily as a sentence, so everything after the type word is
// hidden: however the command is wrapped (sudo, env, time, a subshell, a
// shell string) and whether or not it parses as a screen action. Only the
// overlay's count of typed characters says how much was typed.

// typers are the commands whose `type` subcommand types its arguments.
var typers = []string{"deskyard", "xdotool"}

// isTyper reports whether a word runs a typer, however it is opened:
// deskyard, /usr/bin/xdotool, (deskyard, $(deskyard, {deskyard.
func isTyper(w string) bool {
	return slices.Contains(typers, path.Base(strings.TrimLeft(w, "$({\\")))
}

// typedFrom is the index of the argv word after which everything is typed
// text, or -1: a `type` word after a typer.
func typedFrom(argv []string) int {
	i := slices.IndexFunc(argv, isTyper)
	if i < 0 {
		return -1
	}
	if k := slices.Index(argv[i+1:], "type"); k >= 0 {
		return i + 1 + k
	}
	return -1
}

// cmdEnd are the characters that end a simple command in shell text: a
// separator, or the end of a subshell.
const cmdEnd = ";&|\n)`"

// maskTyped hides what a typer types in shell text, up to the end of that
// command. Quotes don't hide a typer from it: in `bash -lc 'deskyard type
// …'` (a display form, quoted again) the inner command is found too.
func maskTyped(s string) string {
	var b strings.Builder
	last := 0      // s is written up to here
	typer := false // a typer runs in the current command
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case strings.IndexByte(cmdEnd, c) >= 0:
			typer = false
			i++
			continue
		case strings.IndexByte(" \t'\"", c) >= 0:
			i++
			continue
		}
		j := i
		for j < len(s) && strings.IndexByte(cmdEnd+" \t'\"", s[j]) < 0 {
			j++
		}
		switch w := s[i:j]; {
		case !typer:
			typer = isTyper(w)
		case w == "type":
			end := typedEnd(s, j)
			if strings.TrimSpace(s[j:end]) != "" {
				b.WriteString(s[last:j])
				b.WriteString(" " + masked)
				if end < len(s) && (s[end-1] == ' ' || s[end-1] == '\t') {
					b.WriteByte(' ')
				}
				last = end
			}
			typer = false
			j = end
		}
		i = j
	}
	b.WriteString(s[last:])
	return b.String()
}

// typedEnd is where the typed text from i ends: at the first unquoted cmdEnd
// character, or the end of s.
func typedEnd(s string, i int) int {
	var quote byte
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' {
				i++
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '\\':
			i++
		case strings.IndexByte(cmdEnd, c) >= 0:
			return i
		}
	}
	return len(s)
}

// DisplayCommand is argv as one masked line for people to read: values after
// secret flags, secret-looking strings and typed text are •••, arguments
// with spaces or quotes are single-quoted, and it is cut at 120 characters
// with "…".
func DisplayCommand(argv []string) string {
	var b strings.Builder
	hide := false
	withMysql := hasMysql(argv)
	typed := typedFrom(argv)
	for i, arg := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		if typed >= 0 && i > typed {
			b.WriteString(masked) // all of the typed text, as one
			break
		}
		arg = plainText(cutRunes(arg, maxArg)) // keeps newlines for the masking pass
		var next string
		if i+1 < len(argv) {
			next = argv[i+1]
		}
		raw := arg
		if hide {
			arg = masked
		} else {
			arg = flatten(maskText(arg, withMysql))
		}
		hide = (longFlag.MatchString(raw) && secretName(raw)) || raw == "aws_secret_access_key" ||
			(raw == "-p" && withMysql) ||
			((raw == "-u" || raw == "--user") && userValue(next))
		b.WriteString(quote(arg))
		if utf8.RuneCountInString(b.String()) > maxDisplay {
			break
		}
	}
	s := b.String()
	if utf8.RuneCountInString(s) > maxDisplay {
		return cutRunes(s, maxDisplay) + "…"
	}
	return s
}

// DisplayPath is a file path for people to read: control and format
// characters are gone, and a path longer than 120 characters keeps its end
// (the file name), behind "…".
func DisplayPath(p string) string {
	p = strings.TrimSpace(flatten(plainText(cutRunes(p, maxArg))))
	if n := utf8.RuneCountInString(p); n > maxDisplay {
		r := []rune(p)
		return "…" + string(r[n-maxDisplay:])
	}
	return p
}

// plainText replaces tabs and other control characters with spaces, line
// breaks with \n (so the masking pass still sees where a command ends; see
// flatten), and drops invalid UTF-8
// and Unicode format characters (such as the bidi overrides), so a string
// can't inject into a log or the UI, or split a secret to dodge masking.
func plainText(s string) string {
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\r', r == '\u2028', r == '\u2029', r == '\u0085':
			return '\n'
		case unicode.IsControl(r):
			return ' '
		case unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
}

// flatten turns the line breaks plainText kept into spaces, after masking.
func flatten(s string) string { return strings.ReplaceAll(s, "\n", " ") }

func quote(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, " \t\r\n'\"") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// cutRunes keeps the first n runes of s.
func cutRunes(s string, n int) string {
	if len(s) <= n { // fewer bytes, so fewer runes
		return s
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}
