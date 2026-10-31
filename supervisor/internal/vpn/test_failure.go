package vpn

import "errors"

// These errors are fixed, actionable messages. Public test jobs may show only
// this allowlist, never arbitrary provider responses, engine errors or wrappers.
var (
	ErrTailscaleTags          = errors.New("Tailscale rejected the device tags. Define them in the tailnet access policy and allow this OAuth client to use them.")
	ErrTailscaleAuthorization = errors.New("Tailscale authorization failed; check OAuth scopes and device tags")
	ErrTailscaleRateLimit     = errors.New("Tailscale API rate limit reached; retry later")
)

// TestFailureMessage strips private wrapper/context errors and passes through
// only recognized fixed messages. Unknown failures retain a generic diagnostic.
func TestFailureMessage(err error) string {
	for _, known := range []error{ErrTailscaleTags, ErrTailscaleAuthorization, ErrTailscaleRateLimit} {
		if errors.Is(err, known) {
			return "Connection test failed: " + known.Error()
		}
	}
	return "Connection test failed; check provider credentials, permissions, device approval and host internet access"
}
