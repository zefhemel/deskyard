package terminal

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestSessionNames(t *testing.T) {
	for _, s := range []string{"main", "build-2", "A_b", strings.Repeat("a", 64)} {
		if !ValidSession(s) {
			t.Errorf("rejected %q", s)
		}
	}
	for _, s := range []string{"", "-x", "a.b", "a:b", "a b", "a;id", "é", strings.Repeat("a", 65)} {
		if ValidSession(s) {
			t.Errorf("accepted %q", s)
		}
	}
}
func TestCommand(t *testing.T) {
	if got := Command("build-2"); !reflect.DeepEqual(got, []string{"deskyard", "terminal-helper", "build-2"}) {
		t.Fatal(got)
	}
}

func TestAttachCanceledBeforeStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Attach(ctx, "main", nil, io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("wanted cancellation before filesystem or process work, got %v", err)
	}
}

func TestAttachmentIdentity(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	if !ValidAttachment(id) {
		t.Fatal("valid random identity rejected")
	}
	for _, bad := range []string{"", "main", "../bad", strings.Repeat("x", 32), id + "a"} {
		if ValidAttachment(bad) {
			t.Fatalf("invalid identity accepted %q", bad)
		}
	}
	if ValidSession(attachmentSession(id)) {
		t.Fatal("internal session exposed in public namespace")
	}
}
