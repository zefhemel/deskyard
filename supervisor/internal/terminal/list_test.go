package terminal

import "testing"

func TestParseSessions(t *testing.T) {
	sessions, err := ParseSessions([]byte("zebra\n_deskyard_0123456789abcdef0123456789abcdef\nmain\n"))
	if err != nil || len(sessions) != 2 || sessions[0].Name != "main" || sessions[1].Name != "zebra" {
		t.Fatalf("%+v %v", sessions, err)
	}
	for _, value := range []string{"bad.name\n", "main\nmain\n", "_unknown\n"} {
		if _, err := ParseSessions([]byte(value)); err == nil {
			t.Fatal(value)
		}
	}
	empty, err := ParseSessions(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
}
