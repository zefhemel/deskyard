package auth

// Action is something an account wants to do; Can decides it, so the
// permissions table of the spec lives in one place.
type Action int

const (
	View           Action = iota // use a computer fully: its desktop, terminal, processes and activity
	Power                        // start, stop, restart
	Manage                       // create, remove, change settings, upgrade
	ManageAccounts               // accounts, access, image catalog
	Share                        // share a computer with a new token account
	Observe                      // watch a computer's desktop, nothing else
)

// Level is how far a grant lets an account use a computer.
type Level string

const (
	LevelNone    Level = ""
	LevelObserve Level = "observe"
	LevelFull    Level = "full"
)

// ValidLevel reports whether l is a level a grant can carry.
func ValidLevel(l Level) bool { return l == LevelObserve || l == LevelFull }

// Subject is what Can needs to know about an account.
type Subject interface {
	IsAdmin() bool
	Level(name string) Level
	IsShare() bool
}

// Can decides action on the named computer ("" for none).
func Can(a Subject, action Action, name string) bool {
	if a.IsAdmin() {
		return true
	}
	if name == "" {
		return false
	}
	switch action {
	case Observe:
		return a.Level(name) != LevelNone
	case View, Power:
		return a.Level(name) == LevelFull
	case Share:
		// Shares can't share.
		return a.Level(name) == LevelFull && !a.IsShare()
	default:
		return false
	}
}
