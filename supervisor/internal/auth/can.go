package auth

import "errors"

// Action is something an account wants to do; Check decides it, so the
// permissions table of the spec lives in one place.
type Action int

const (
	View           Action = iota // use an environment fully: its desktop, terminal, processes and activity
	Power                        // start, stop, restart
	Manage                       // create, remove, change settings, upgrade
	ManageAccounts               // accounts, access, image catalog: what only admins do
	Share                        // share an environment with a new token account
	Observe                      // watch an environment's desktop, nothing else
	UseCLI                       // sign in a CLI (no environment): an admin, or full on some environment
	WebDAV                       // use an environment's files over WebDAV ("": WebDAV at all)
)

// Level is how far a grant lets an account use an environment.
type Level string

const (
	LevelNone    Level = ""
	LevelObserve Level = "observe"
	LevelFull    Level = "full"
)

// ValidLevel reports whether l is a level a grant can carry.
func ValidLevel(l Level) bool { return l == LevelObserve || l == LevelFull }

func (l Level) rank() int {
	switch l {
	case LevelFull:
		return 2
	case LevelObserve:
		return 1
	}
	return 0
}

// AtLeast reports whether l allows everything o does.
func (l Level) AtLeast(o Level) bool { return l.rank() >= o.rank() }

// Subject is what Check needs to know about an account.
type Subject interface {
	IsAdmin() bool
	IsShare() bool
	HasPassword() bool
	Level(name string) Level
	// Environments lists the environments the account has a grant of.
	Environments() []string
}

// Why an action is refused.
var (
	// ErrNoAccess: the account has no grant of the environment at all; it
	// isn't told whether the environment exists.
	ErrNoAccess        = errors.New("no such environment")
	ErrAdminOnly       = errors.New("only an admin can do that")
	ErrObserveOnly     = errors.New("observe-only access can't do that") // full access would allow it
	ErrSharesCantShare = errors.New("shares can't share")
	// ErrBrowserOnly refuses a CLI to an account that may only watch: the
	// CLI can do nothing at observe level.
	ErrBrowserOnly  = errors.New("observe-only access is for the browser")
	ErrPasswordOnly = errors.New("only accounts with a password can use WebDAV")
)

// Check decides action on the named environment ("" for none): nil when
// allowed, otherwise why not.
func Check(a Subject, action Action, name string) error {
	if action == WebDAV {
		// Not shares, and not token accounts, admins included: WebDAV
		// signs in with a password.
		if !a.HasPassword() || a.IsShare() {
			return ErrPasswordOnly
		}
		if name == "" {
			return nil
		}
		action = View
	}
	if a.IsAdmin() {
		return nil
	}
	if action == UseCLI {
		for _, env := range a.Environments() {
			if a.Level(env) == LevelFull {
				return nil
			}
		}
		return ErrBrowserOnly
	}
	if name == "" {
		return ErrAdminOnly
	}
	l := a.Level(name)
	if l == LevelNone {
		return ErrNoAccess
	}
	switch action {
	case Observe:
		return nil
	case View, Power:
		if l != LevelFull {
			return ErrObserveOnly
		}
		return nil
	case Share:
		if a.IsShare() {
			return ErrSharesCantShare
		}
		if l != LevelFull {
			return ErrObserveOnly
		}
		return nil
	default:
		return ErrAdminOnly
	}
}

// Can reports whether Check allows action on the named environment.
func Can(a Subject, action Action, name string) bool { return Check(a, action, name) == nil }
