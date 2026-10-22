package activity

import (
	"io/fs"
	"path"
	"strconv"
	"strings"
)

const (
	maxWalk     = 32 // ancestors looked at
	maxCallerRn = 40 // longest program name
)

var shells = []string{"bash", "sh", "dash", "zsh", "fish", "ksh"}

type procInfo struct {
	ppid  int
	argv  []string
	comm  string
	shell bool
	tty   bool
}

// CallerName names the program that ran this command, for people to read:
// "Claude Code", "Codex", "a terminal" for an interactive shell, or the
// parent program's name. pid is the caller (os.Getppid()) and proc is the
// /proc file system. A pid that can't be read counts as "a terminal".
func CallerName(proc fs.FS, pid int) string {
	var chain []procInfo
	seen := map[int]bool{}
	for p := pid; p > 0 && len(chain) < maxWalk && !seen[p]; {
		seen[p] = true
		info, ok := readProc(proc, p)
		if !ok {
			break
		}
		chain = append(chain, info)
		p = info.ppid
	}
	if len(chain) == 0 {
		return "a terminal"
	}
	// A known agent anywhere above wins.
	for _, c := range chain {
		if n := knownAgent(c); n != "" {
			return n
		}
	}
	outer := ""
	for _, c := range chain {
		name := programName(c)
		if !c.shell {
			return cutRunes(name, maxCallerRn)
		}
		if c.tty {
			return "a terminal"
		}
		outer = name
	}
	return cutRunes(outer, maxCallerRn)
}

func knownAgent(c procInfo) string {
	names := []string{c.comm}
	for i, a := range c.argv {
		if i < 2 {
			names = append(names, path.Base(a))
		}
		if strings.Contains(a, "@anthropic-ai/claude-code") {
			return "Claude Code"
		}
	}
	for _, n := range names {
		switch n {
		case "claude":
			return "Claude Code"
		case "codex":
			return "Codex"
		}
	}
	return ""
}

func programName(c procInfo) string {
	n := ""
	if len(c.argv) > 0 {
		n = strings.TrimPrefix(path.Base(c.argv[0]), "-")
	}
	if n == "" || n == "." || n == "/" {
		n = c.comm
	}
	if n == "" {
		n = "a program"
	}
	return n
}

func readProc(proc fs.FS, pid int) (procInfo, bool) {
	dir := strconv.Itoa(pid)
	stat, err := fs.ReadFile(proc, dir+"/stat")
	if err != nil {
		return procInfo{}, false
	}
	s := string(stat)
	// "pid (comm) S ppid ...": comm may hold spaces and parentheses.
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return procInfo{}, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return procInfo{}, false
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return procInfo{}, false
	}
	info := procInfo{ppid: ppid}
	if b, err := fs.ReadFile(proc, dir+"/cmdline"); err == nil {
		for _, a := range strings.Split(strings.TrimRight(string(b), "\x00"), "\x00") {
			if a != "" {
				info.argv = append(info.argv, a)
			}
		}
	}
	if b, err := fs.ReadFile(proc, dir+"/comm"); err == nil {
		info.comm = strings.TrimSpace(string(b))
	}
	info.shell = isShell(info)
	if info.shell {
		if t, err := fs.ReadLink(proc, dir+"/fd/0"); err == nil {
			info.tty = strings.HasPrefix(t, "/dev/pts/") || strings.HasPrefix(t, "/dev/tty")
		}
	}
	return info, true
}

func isShell(c procInfo) bool {
	n := programName(c)
	for _, s := range shells {
		if n == s {
			return true
		}
	}
	return false
}
