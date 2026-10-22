package desktops

import (
	"fmt"
	"regexp"
	"strings"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

// Reserved names are the supervisor's own top-level routes.
var reserved = map[string]bool{
	"api": true, "login": true, "logout": true, "assets": true, "static": true,
	"healthz": true, "favicon.ico": true, "robots.txt": true,
	// The UI's own pages.
	"desktops": true, "images": true, "accounts": true,
}

// ValidName checks a desktop name: DNS label rules (so it also works as a
// container, network and folder name), not one of the supervisor's routes.
func ValidName(name string) error {
	if strings.HasPrefix(name, "_") || reserved[name] {
		return fmt.Errorf("%q is reserved by the supervisor", name)
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("a name is 2 to 32 characters: lowercase letters, digits and '-', starting with a letter and not ending with '-'")
	}
	return nil
}

// IsReserved reports whether a top-level path segment belongs to the supervisor.
func IsReserved(segment string) bool {
	return reserved[segment] || strings.HasPrefix(segment, "_")
}
