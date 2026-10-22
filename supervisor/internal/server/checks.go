package server

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// scanScript runs as root in a throwaway container of an image and prints one
// "<kind> <path>" line per finding:
//   - sudo: a sudo or doas executable anywhere;
//   - setuid: a setuid or setgid file anywhere;
//   - writable: a file or folder that user (UID 1000, or one of its groups)
//     can write, under the paths root runs from. The home template is meant
//     to belong to user.
//
// -xdev keeps find on the image's own filesystem (no /proc, /sys, volumes).
const scanScript = `
gids=$(id -G user 2>/dev/null || echo 1000)
grp=""
for g in $gids; do grp="$grp -o ( -group $g -perm -g=w )"; done
find / -xdev \( -name sudo -o -name sudoedit -o -name doas \) \( -type l -o -type f -perm /111 \) -printf 'sudo %p\n' 2>/dev/null
find / -xdev ! -type l \( -perm -4000 -o -perm -2000 \) -printf 'setuid %p\n' 2>/dev/null
for p in /usr /etc /opt/workspace /opt/deskyard; do
  [ -e "$p" ] || continue
  find "$p" -xdev -path ` + homeTemplate + ` -prune -o ! -type l \( -user 1000 -o -perm -o=w $grp \) -printf 'writable %p\n' 2>/dev/null
done
exit 0
`

var scanReasons = map[string]string{
	"sudo":     "sudo or doas",
	"setuid":   "setuid or setgid",
	"writable": "writable by user",
}

// rootScan compares the new image with its base: the new one may add no
// sudo/doas, no setuid/setgid file and nothing user can write where root runs
// from. The error names every offending path.
func (s *Server) rootScan(ctx context.Context, baseID, newID string) error {
	before, err := s.scanImage(ctx, baseID)
	if err != nil {
		return fmt.Errorf("scanning the base: %w", err)
	}
	after, err := s.scanImage(ctx, newID)
	if err != nil {
		return fmt.Errorf("scanning the image: %w", err)
	}
	found := map[string][]string{}
	for f := range after {
		if !before[f] {
			kind, path, _ := strings.Cut(f, " ")
			found[path] = append(found[path], scanReasons[kind])
		}
	}
	if len(found) == 0 {
		return nil
	}
	paths := make([]string, 0, len(found))
	for p := range found {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString("the image would give user a way to become root:")
	for _, p := range paths {
		reasons := found[p]
		slices.Sort(reasons)
		fmt.Fprintf(&b, "\n  %s (%s)", p, strings.Join(reasons, ", "))
	}
	return fmt.Errorf("%s", b.String())
}

// scanImage runs scanScript in a throwaway container of ref, with no network.
func (s *Server) scanImage(ctx context.Context, ref string) (map[string]bool, error) {
	id, err := s.Eng.Create(ctx, engine.ContainerSpec{
		Name: "deskyard-scan-" + randomID(8), Image: ref, Network: "none", Restart: "no",
		Entrypoint: []string{"sleep", "infinity"},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Eng.Remove(context.WithoutCancel(ctx), id) }()
	if err := s.Eng.Start(ctx, id); err != nil {
		return nil, err
	}
	out, errOut, code, err := engine.Output(ctx, s.Eng, id, engine.ExecSpec{Cmd: []string{"sh", "-c", scanScript}, User: "root"})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("the scan failed (%d): %s", code, strings.TrimSpace(string(errOut)))
	}
	found := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if kind, path, ok := strings.Cut(line, " "); ok && scanReasons[kind] != "" && path != "" {
			found[line] = true
		}
	}
	return found, nil
}

// smokeTestDesktop starts a throwaway desktop of the image, waits until it
// answers (as a new desktop would: 2 minutes), then removes it with its folder.
func (s *Server) smokeTestDesktop(ctx context.Context, imageID string) error {
	name := "build-check-" + randomID(6)
	defer func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if err := s.Desktops.Remove(bg, name, true); err != nil && err != desktops.ErrNotFound {
			s.Log.Warn("removing the test desktop", "desktop", name, "err", err)
		}
	}()
	_, err := s.Desktops.Create(ctx, name, desktops.Settings{Image: imageID}, func(string, float64) {})
	return err
}
