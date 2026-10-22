package auth

// Action is something an account wants to do; Can decides it, so the
// permissions table of the spec lives in one place.
type Action int

const (
	View           Action = iota // see and open a desktop, its processes and activity
	Power                        // start, stop, restart
	Manage                       // create, remove, change settings, upgrade
	ManageAccounts               // accounts, access, image catalog
	CreateToken                  // create agent tokens, revoke own
	RevokeAnyToken
)

// Subject is what Can needs to know about an account.
type Subject interface {
	IsAdmin() bool
	HasAccess(desktopID string) bool
}

func Can(a Subject, action Action, desktopID string) bool {
	if a.IsAdmin() {
		return true
	}
	switch action {
	case View, Power, CreateToken:
		return desktopID != "" && a.HasAccess(desktopID)
	default:
		return false
	}
}
