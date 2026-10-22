package activity

import (
	"regexp"
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
	// -u and --user take user:password (curl, and others).
	secretFlag = regexp.MustCompile(`^(?i:-p|-u|--user|aws_secret_access_key|--[a-z0-9-]*(?:` + secretWords + `))$`)

	// -pSECRET and -uUSER:SECRET attached to the flag, and the short flags
	// with their value in the same string (a script in sh -c). Any -p or -u
	// word is masked, so `mkdir -pv` and `sort -u f` lose a word: hiding when
	// in doubt beats naming argv[0]s.
	shortFlag = regexp.MustCompile(`(^|[\s'"])(-[pu]\s*)\S+`)

	// A secret flag with its value in the same string: --token=abc, --secret abc.
	flagValue = regexp.MustCompile(`(?i)(--(?:user|[a-z0-9-]*(?:` + secretWords + `))(?:=|\s+))\S+`)

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
func MaskText(s string) string {
	s = pem.ReplaceAllString(s, masked)
	s = awsSecret.ReplaceAllString(s, "${1}"+masked)
	s = awsKeyID.ReplaceAllString(s, masked)
	s = flagValue.ReplaceAllString(s, "${1}"+masked)
	s = shortFlag.ReplaceAllString(s, "${1}${2}"+masked)
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
	for i, arg := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		arg = plainText(cutRunes(arg, maxArg))
		switch {
		case hide:
			arg = masked
		default:
			arg = MaskText(arg)
		}
		hide = secretFlag.MatchString(arg)
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
