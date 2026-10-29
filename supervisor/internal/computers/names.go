package computers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/envname"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

// ValidName checks a computer name: DNS label rules (so it also works as a
// container, network and folder name), not one of the supervisor's routes.
func ValidName(name string) error {
	if envname.IsReserved(name) {
		return fmt.Errorf("%q is reserved by the supervisor", name)
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("a name is 2 to 32 characters: lowercase letters, digits and '-', starting with a letter and not ending with '-'")
	}
	return nil
}

// SmokePrefix starts the names of the throwaway computers that test a freshly
// built image; nobody else may use it.
const SmokePrefix = "build-check-"

// ValidNewName is ValidName for a computer someone creates.
func ValidNewName(name string) error {
	if strings.HasPrefix(name, SmokePrefix) {
		return fmt.Errorf("names starting with %q are reserved by the supervisor", SmokePrefix)
	}
	return ValidName(name)
}

// IsReserved reports whether a top-level path segment belongs to the supervisor.
func IsReserved(segment string) bool { return envname.IsReserved(segment) }
