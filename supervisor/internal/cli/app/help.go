package app

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

//go:embed guide.txt
var guideText string

func (a *App) writeOverview(w io.Writer) {
	fmt.Fprint(w, `deskyard: use a Deskyard desktop (a Linux desktop with a shared browser) from inside it or from anywhere else.

Usage: deskyard [-d NAME] COMMAND [ARGS...]

New here? Run `+"`deskyard guide`"+` (written for AI agents) and `+"`deskyard COMMAND --help`"+`.
`)
	for _, g := range groups {
		fmt.Fprintf(w, "\n%s:\n", g.Title)
		for _, c := range visibleCommands() {
			if c.Group == g.Name {
				fmt.Fprintf(w, "  %-36s %s\n", c.Usage, c.Summary)
			}
		}
	}
	fmt.Fprint(w, `
Choosing a desktop:
  Inside a desktop, commands act on it directly. Elsewhere they use a remote:
  -d NAME, else $DESKYARD_REMOTE, else $DESKYARD_URL + $DESKYARD_TOKEN,
  else the default remote (`+"`deskyard remote default`"+`).

Environment:
`)
	for _, e := range environment {
		fmt.Fprintf(w, "  %-16s %s\n", e.Name, e.Desc)
	}
	fmt.Fprint(w, "\nExit codes:\n")
	for _, e := range exitCodes {
		fmt.Fprintf(w, "  %-6s %s\n", e.Code, e.Meaning)
	}
	fmt.Fprint(w, "\nMachine-readable: deskyard describe --json\n")
}

func (a *App) printHelp(names []string) error {
	if len(names) == 0 {
		a.writeOverview(a.Stdout)
		return nil
	}
	name := strings.Join(names, " ")
	if cmd := findCommand(name); cmd != nil {
		writeCommandHelp(a.Stdout, cmd)
		return nil
	}
	if subs := subcommands(name); len(subs) > 0 {
		fmt.Fprintf(a.Stdout, "deskyard %s: subcommands\n\n", name)
		for _, s := range subs {
			c := findCommand(name + " " + s)
			fmt.Fprintf(a.Stdout, "  desktop %-34s %s\n", c.Usage, c.Summary)
		}
		fmt.Fprintf(a.Stdout, "\nRun `deskyard %s SUBCOMMAND --help` for details.\n", name)
		return nil
	}
	return usagef(nil, "no command %q", name)
}

func writeCommandHelp(w io.Writer, c *Command) {
	fmt.Fprintf(w, "deskyard %s: %s\n\n", c.Name, c.Summary)
	fmt.Fprintf(w, "Usage: deskyard [-d NAME] %s\n\n", c.Usage)
	fmt.Fprintf(w, "%s\n", wrap(c.Desc, 78, ""))
	if len(c.Args) > 0 {
		fmt.Fprint(w, "\nArguments:\n")
		for _, a := range c.Args {
			req := "optional"
			if a.Required {
				req = "required"
			}
			typ := a.Type
			if a.Variadic {
				typ += "..."
			}
			desc := a.Desc
			if len(a.Enum) > 0 {
				desc += " (" + strings.Join(a.Enum, ", ") + ")"
			}
			if a.Default != "" {
				desc += "; default " + a.Default
			}
			fmt.Fprintf(w, "  %-10s %-11s %-9s %s\n", strings.ToUpper(a.Name), typ, req, desc)
		}
	}
	if len(c.Flags) > 0 {
		fmt.Fprint(w, "\nOptions:\n")
		for _, f := range c.Flags {
			name := f.Name
			if f.Short != "" {
				name = f.Short + ", " + f.Name
			}
			if f.Value != "" {
				name += " " + f.Value
			}
			desc := f.Desc
			if f.Default != "" {
				desc += " (default " + f.Default + ")"
			}
			fmt.Fprintf(w, "  %-20s %s\n", name, wrap(desc, 56, strings.Repeat(" ", 23)))
		}
	}
	fmt.Fprintf(w, "\nPrints: %s\n", wrap(c.Output.Desc, 70, "  "))
	fmt.Fprint(w, "\nExamples:\n")
	for _, e := range c.Examples {
		fmt.Fprintf(w, "  %s\n", e)
	}
	fmt.Fprintf(w, "\nWhere: %s\n", c.Where)
	fmt.Fprintf(w, "Exit codes: %s\n", c.Exit)
}

// wrap breaks text into lines of at most width runes; continuation lines
// start with indent.
func wrap(s string, width int, indent string) string {
	var b strings.Builder
	line := 0
	for i, word := range strings.Fields(s) {
		n := len([]rune(word))
		if i > 0 {
			if line+1+n > width {
				b.WriteString("\n" + indent)
				line = 0
			} else {
				b.WriteByte(' ')
				line++
			}
		}
		b.WriteString(word)
		line += n
	}
	return b.String()
}

func runHelp(a *App, c *call) error { return a.printHelp(c.args) }

func runVersion(a *App, _ *call) error {
	fmt.Fprintf(a.Stdout, "deskyard %s\n", a.version())
	return nil
}

func (a *App) version() string {
	if a.Version == "" {
		return "dev"
	}
	return a.Version
}

func runGuide(a *App, _ *call) error {
	_, err := io.WriteString(a.Stdout, guideText)
	return err
}

type describeDoc struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Description string     `json:"description"`
	Usage       string     `json:"usage"`
	GlobalFlags []Flag     `json:"globalFlags"`
	Environment any        `json:"environment"`
	ExitCodes   any        `json:"exitCodes"`
	Commands    []*Command `json:"commands"`
}

func runDescribe(a *App, _ *call) error {
	doc := describeDoc{
		Name:        "deskyard",
		Version:     a.version(),
		Description: "Use a Deskyard desktop (a Linux desktop with a shared Chromium) from inside it or remotely through its agent API. Run commands as `deskyard [-d NAME] <name> <arguments...>`; arguments are positional in the listed order, flags go after the command name.",
		Usage:       "deskyard [-d NAME] COMMAND [ARGS...]",
		GlobalFlags: globalFlags,
		Environment: environment,
		ExitCodes:   exitCodes,
		Commands:    visibleCommands(),
	}
	for _, c := range doc.Commands {
		if c.Args == nil {
			c.Args = []Arg{}
		}
		if c.Flags == nil {
			c.Flags = []Flag{}
		}
	}
	enc := json.NewEncoder(a.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
