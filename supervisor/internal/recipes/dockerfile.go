package recipes

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

const (
	flatpakReal = "/usr/bin/flatpak.real"
	remotesDir  = "/etc/deskyard/flatpak-remotes"
	// Where the base image keeps Flathub's repo file.
	flathubRepo = "/etc/workspace/flathub.flatpakrepo"
)

var baseIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]*$`)

// Dockerfile generates the build for r on the image baseID. It returns "" if
// r is invalid or baseID isn't a plain image ID or reference, so call
// r.Validate first for a proper error.
//
// Everything admin-supplied is either validated to a character set that is
// safe inside single quotes (names, specs, URLs) or travels base64-encoded
// (the build commands), so no recipe can add Dockerfile instructions or
// break out of a shell word. The base image sets no USER, so the final user
// is root, as in the base; ENTRYPOINT and CMD are inherited untouched.
func Dockerfile(r Recipe, baseID string, flatpakInBuild bool) string {
	if r.Validate() != nil || !baseIDRE.MatchString(baseID) {
		return ""
	}
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	line("FROM %s", baseID)
	line("USER root")

	if len(r.Mise) > 0 {
		line("")
		line("# mise tools, with their executables linked into /opt/deskyard/bin")
		line("RUN %s", miseStep(r.Mise))
	}

	if len(r.Remotes) > 0 {
		line("")
		line("# Flatpak remotes")
		for _, rm := range r.Remotes {
			line("RUN %s", remoteStep(rm))
		}
	}

	if flatpakInBuild && len(r.Flatpaks) > 0 {
		line("")
		line("# Flatpak apps, system-wide")
		line("RUN %s", flatpakStep(r.Flatpaks))
	}

	if r.HomeFrom == HomeFromImage {
		line("")
		line("# Starting home for new desktops")
		line("COPY home-template/ /opt/deskyard/home-template/")
		line("RUN chown -R 1000:1000 /opt/deskyard/home-template")
	}

	if strings.TrimSpace(r.Commands) != "" {
		line("")
		line("# Build commands (base64 keeps them out of the Dockerfile's own syntax)")
		line("RUN %s", commandsStep(r.Commands))
	}
	return b.String()
}

func miseStep(specs []string) string {
	var quoted, installs []string
	for _, s := range specs {
		q := "'" + withVersion(s) + "'"
		quoted = append(quoted, q)
		installs = append(installs, q)
	}
	var b strings.Builder
	b.WriteString("set -eux; ")
	b.WriteString("export MISE_YES=1 MISE_CACHE_DIR=/tmp/mise-cache MISE_DATA_DIR=/opt/mise MISE_CONFIG_DIR=/tmp/mise-config; ")
	b.WriteString("mise install " + strings.Join(installs, " ") + "; ")
	// Ask mise where each tool keeps its executables (npm tools use
	// node_modules/.bin). A tool without any fails the build.
	b.WriteString("for spec in " + strings.Join(quoted, " ") + "; do ")
	b.WriteString("n=0; ")
	b.WriteString(`paths=$(mise bin-paths "$spec"); for d in $paths; do `)
	b.WriteString(`for f in "$d"/*; do `)
	b.WriteString(`if [ -f "$f" ] && [ -x "$f" ]; then ln -sf "$f" "/opt/deskyard/bin/$(basename "$f")"; n=$((n+1)); fi; `)
	b.WriteString("done; done; ")
	b.WriteString(`if [ "$n" -eq 0 ]; then echo "mise tool $spec has no executables to link" >&2; exit 1; fi; `)
	b.WriteString("done; ")
	b.WriteString("rm -rf /tmp/mise-cache /tmp/mise-config")
	return b.String()
}

// withVersion gives a tool without a version "@latest", which mise install
// and mise bin-paths both resolve without a config file. The "@" of
// "npm:@scope/pkg" starts a scope, not a version.
func withVersion(spec string) string {
	i := strings.LastIndex(spec, "@")
	if i > 0 && spec[i-1] != ':' && spec[i-1] != '/' && !strings.Contains(spec[i:], "/") && i < len(spec)-1 {
		return spec
	}
	return strings.TrimSuffix(spec, "@") + "@latest"
}

func remoteStep(rm Remote) string {
	file := remotesDir + "/" + rm.Name + ".flatpakrepo"
	var b strings.Builder
	b.WriteString("set -eux; ")
	if rm.RepoFile != "" {
		fmt.Fprintf(&b, "curl -fsSL -o '%s' '%s'; ", file, rm.RepoFile)
	} else {
		fmt.Fprintf(&b, `printf '[Flatpak Repo]\nTitle=%%s\nUrl=%%s\nGPGKey=%%s\n' '%s' '%s' '%s' > '%s'; `,
			rm.Name, rm.URL, cleanKey(rm.GPGKey), file)
	}
	fmt.Fprintf(&b, "%s --system remote-add --if-not-exists '%s' '%s'", flatpakReal, rm.Name, file)
	return b.String()
}

func flatpakStep(apps []Flatpak) string {
	var b strings.Builder
	b.WriteString("set -eux; ")
	// Always: apps from other remotes and .flatpakref files take their
	// runtimes from Flathub.
	fmt.Fprintf(&b, "%s --system remote-add --if-not-exists flathub %s; ", flatpakReal, flathubRepo)
	hasRef := false
	for _, a := range apps {
		if a.Ref != "" {
			hasRef = true
			fmt.Fprintf(&b, "%s --system install -y --noninteractive --from '%s'; ", flatpakReal, a.Ref)
			continue
		}
		remote := a.Remote
		if remote == "" {
			remote = defaultRemote
		}
		fmt.Fprintf(&b, "%s --system install -y --noninteractive '%s' '%s'; ", flatpakReal, remote, a.ID)
	}
	if hasRef {
		// A .flatpakref adds its own remote. Save it with the others, so
		// desktops can update from it (workspace-init adds these per user).
		fmt.Fprintf(&b, "%s", saveRemotesSnippet)
	}
	return strings.TrimSuffix(b.String(), "; ")
}

func commandsStep(commands string) string {
	script := strings.ReplaceAll(commands, "\r\n", "\n")
	enc := base64.StdEncoding.EncodeToString([]byte(script))
	return fmt.Sprintf("set -eux; printf %%s '%s' | base64 -d > /tmp/deskyard-commands.sh; bash -eux /tmp/deskyard-commands.sh; rm -f /tmp/deskyard-commands.sh", enc)
}

// saveRemotesSnippet writes a .flatpakrepo for each system remote that has
// none yet (those a .flatpakref added), with the remote's GPG key. A remote
// without a key is skipped: workspace-init would turn it into an unsigned
// or broken user remote.
var saveRemotesSnippet = flatpakReal + ` --system remotes --columns=name,url | while read -r name url; do ` +
	`f="` + remotesDir + `/$name.flatpakrepo"; ` +
	`k="/var/lib/flatpak/repo/$name.trustedkeys.gpg"; ` +
	`if [ "$name" = flathub ] || [ -e "$f" ]; then continue; fi; ` +
	`if [ -s "$k" ]; then printf '[Flatpak Repo]\nTitle=%s\nUrl=%s\nGPGKey=%s\n' "$name" "$url" "$(base64 -w0 "$k")" > "$f"; ` +
	`else echo "not saving flatpak remote $name: it has no GPG key, so desktops could not verify it" >&2; fi; ` +
	`done; `
