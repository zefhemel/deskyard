package terminal

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseSessions(t *testing.T) {
	sessions, err := ParseSessions([]byte("zebra\t0\tbash\n_deskyard_0123456789abcdef0123456789abcdef\t1\tbash\nmain\t2\tvim\n"))
	want := []Session{{Name: "main", Clients: 2, Command: "vim"}, {Name: "zebra", Clients: 0, Command: "bash"}}
	if err != nil || !reflect.DeepEqual(sessions, want) {
		t.Fatalf("%+v %v", sessions, err)
	}
	for _, value := range []string{"bad.name\t0\tbash\n", "main\t0\tbash\nmain\t0\tbash\n", "_unknown\t0\tbash\n", "main\n", "main\t-1\tbash\n", "main\tx\tbash\n", "main\t0\tba\x01sh\n"} {
		if _, err := ParseSessions([]byte(value)); err == nil {
			t.Fatalf("%q", value)
		}
	}
	empty, err := ParseSessions(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
}

func TestParseSessionDetails(t *testing.T) {
	lease := "_deskyard_0123456789abcdef0123456789abcdef"
	other := "_deskyard_fedcba9876543210fedcba9876543210"
	// A browser lease groups with its base session: both clients count.
	sessions := parseTmuxSessions([]byte("main|main|1|bash\n" + lease + "|main|2|bash\n"))
	if !reflect.DeepEqual(sessions, []Session{{Name: "main", Clients: 3, Command: "bash"}}) {
		t.Fatalf("%+v", sessions)
	}
	// A private group whose base is gone is never a public session.
	if sessions := parseTmuxSessions([]byte(other + "|gone|1|bash\n")); len(sessions) != 0 {
		t.Fatalf("%+v", sessions)
	}
	// Unusable rows (malformed, a name a user changed outside the grammar, a
	// duplicate) are skipped; the rest of the list survives.
	sessions = parseTmuxSessions([]byte("main|main|1\nmy work||0|bash\nmain||x|bash\nok||0|bash\nok||1|bash\n"))
	if !reflect.DeepEqual(sessions, []Session{{Name: "ok", Clients: 0, Command: "bash"}}) {
		t.Fatalf("%+v", sessions)
	}
	// Ungrouped sessions count only their own clients; output is sorted, the
	// command keeps any separator a program name contains, and control
	// characters cannot break the helper's line format.
	sessions = parseTmuxSessions([]byte("zebra||0|a|b\nmain||1|v\tim\n"))
	want := []Session{{Name: "main", Clients: 1, Command: "v?im"}, {Name: "zebra", Clients: 0, Command: "a|b"}}
	if !reflect.DeepEqual(sessions, want) {
		t.Fatalf("%+v", sessions)
	}
}

// A socket nobody listens on, or none, means no server; a live one doesn't.
func TestNoServer(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "dysock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "tmux.sock")
	if !noServer(socket) {
		t.Error("a missing socket should mean no server")
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if noServer(socket) {
		t.Error("a listening socket should be a server")
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	if !noServer(socket) {
		t.Error("a left-behind socket should mean no server")
	}
}
