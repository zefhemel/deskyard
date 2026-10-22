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

const secretWords = `password|passwd|pass|pw|token|secret|key|auth|credentials?`

var (
	// A flag whose value is the next argument: -p, --token, --api-key, ...
	secretFlag = regexp.MustCompile(`^(?i:aws_secret_access_key|--[a-z0-9-]*(?:` + secretWords + `))$`)

	// Short flags carry secrets only in some commands, so they are masked
	// by context (see maskShort): -u/--user VALUE when VALUE is user:pass
	// (curl), -p VALUE after mysql and friends, attached -pVALUE when the
	// command is mysql or VALUE looks like a secret.
	userFlag  = regexp.MustCompile(`(^|[\s'"])(-u\s*|--user(?:=|\s+))(\S*:\S*)`)
	pwdAny    = regexp.MustCompile(`(^|[\s'"])(-p\s*)\S+`)
	pwdAttach = regexp.MustCompile(`(^|[\s'"])(-p)(\S{6,})`)
	// Where one simple command ends and the next begins.
	cmdSep = regexp.MustCompile(`&&|\|\||[;|&\n]`)

	// A secret flag with its value in the same string: --token=abc, --secret abc.
	flagValue = regexp.MustCompile(`(?i)(--(?:[a-z0-9-]*(?:` + secretWords + `))(?:=|\s+))\S+`)

	// PEM private keys, whole (or to the end of the string when cut short),
	// and AWS credentials.
	pem       = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	awsSecret = regexp.MustCompile(`(?i)(aws_secret_access_key(?:\s*[=:]\s*|\s+))(?:"[^"]*"?|'[^']*'?|\S+)`)
	awsKeyID  = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)

	// KEY=value where the name looks secret; the value may be quoted.
	assignment = regexp.MustCompile(`\b([A-Za-z0-9_]*(?i:pass|token|secret|key|auth|credential)[A-Za-z0-9_]*=)(?:"[^"]*"?|'[^']*'?|[^\s'"]+)`)

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
func MaskText(s string) string { return maskText(s, "") }

// mysqlLike are the commands whose -p takes the password.
var mysqlLike = []string{"mysql", "mariadb", "mysqldump"}

// maskText masks s; ctx is the command a leading flag belongs to when s is
// one argument of that command's argv (a text that starts with a command
// word of its own uses that instead).
func maskText(s, ctx string) string {
	var b strings.Builder
	last := 0
	for _, loc := range append(cmdSep.FindAllStringIndex(s, -1), []int{len(s), len(s)}) {
		b.WriteString(maskShort(s[last:loc[0]], ctx))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	return maskRest(b.String())
}

// maskShort masks the short-flag secrets of one simple command.
func maskShort(seg, ctx string) string {
	cmd := ctx
	if f := strings.Fields(seg); len(f) > 0 && !strings.HasPrefix(f[0], "-") {
		cmd = path.Base(f[0])
	}
	seg = userFlag.ReplaceAllString(seg, "${1}${2}"+masked)
	if slices.Contains(mysqlLike, cmd) {
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

func maskRest(s string) string {
	s = pem.ReplaceAllString(s, masked)
	s = awsSecret.ReplaceAllString(s, "${1}"+masked)
	s = awsKeyID.ReplaceAllString(s, masked)
	s = flagValue.ReplaceAllString(s, "${1}"+masked)
	s = dyt.ReplaceAllString(s, masked)
	s = sk.ReplaceAllString(s, "${1}"+masked)
	s = gh.ReplaceAllString(s, masked)
	s = ghPat.ReplaceAllString(s, masked)
	s = assignment.ReplaceAllString(s, "${1}"+masked)
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

// DisplayCommand is argv as one masked line for people to read: values after
// secret flags and secret-looking strings are •••, arguments with spaces or
// quotes are single-quoted, and it is cut at 120 characters with "…".
func DisplayCommand(argv []string) string {
	var b strings.Builder
	hide := false
	prog := ""
	if len(argv) > 0 {
		prog = path.Base(argv[0])
	}
	for i, arg := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		arg = plainText(cutRunes(arg, maxArg))
		var next string
		if i+1 < len(argv) {
			next = argv[i+1]
		}
		raw := arg
		if hide {
			arg = masked
		} else {
			arg = maskText(arg, prog)
		}
		hide = secretFlag.MatchString(raw) ||
			(raw == "-p" && slices.Contains(mysqlLike, prog)) ||
			((raw == "-u" || raw == "--user") && strings.Contains(next, ":"))
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

// plainText replaces control characters with spaces and drops invalid UTF-8
// and Unicode format characters (such as the bidi overrides), so a string
// can't inject into a log or the UI, or split a secret to dodge masking.
func plainText(s string) string {
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), r == '\u2028', r == '\u2029':
			return ' '
		case unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
}

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
