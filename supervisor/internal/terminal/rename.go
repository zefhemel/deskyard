package terminal

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Rename changes only the public identity; grouped clients and windows survive.
func Rename(ctx context.Context, name, next string) error {
	if !ValidSession(name) || !ValidSession(next) {
		return fmt.Errorf("invalid terminal session name")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := queryServer(ctx, "display-message", "-p", "-t", "="+name+":", "#{session_id}")
	id := strings.TrimSpace(string(info))
	if err != nil || !strings.HasPrefix(id, "$") {
		return missing(ctx, name, err)
	}
	if name == next {
		return nil
	}
	reserved, err := groupNameExists(ctx, next)
	if err != nil {
		return err
	}
	if reserved {
		return fmt.Errorf("session name %q is reserved by an existing terminal attachment group", next)
	}
	// Keep title changes before the rename so an error cannot report a failed rename after it succeeded.
	if err := tmuxCommand(ctx, "set-option", "-t", id, "set-titles-string", "#{session_name}").Run(); err != nil {
		return err
	}
	if out, err := tmuxCommand(ctx, "rename-session", "-t", id, next).CombinedOutput(); err != nil {
		return fmt.Errorf("rename terminal: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
