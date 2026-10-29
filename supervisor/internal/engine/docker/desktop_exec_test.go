package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestDesktopExecNamespace(t *testing.T) {
	for _, mode := range []string{"legacy", "userns-v1"} {
		t.Run(mode, func(t *testing.T) {
			var got struct {
				Cmd        []string
				User       string
				WorkingDir string
			}
			d := testAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/json"):
					fmt.Fprintf(w, `{"Config":{"Labels":{"deskyard.fuse":%q}}}`, mode)
				case strings.HasSuffix(r.URL.Path, "/exec"):
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					http.Error(w, "stop after capture", http.StatusBadRequest)
				default:
					t.Errorf("unexpected %s", r.URL.Path)
				}
			})
			_, err := d.Exec(context.Background(), "desktop", engine.ExecSpec{DesktopSession: true, User: "user", WorkDir: "/run/workspace/doc/probe", Cmd: []string{"printf", "a b", "$HOME"}})
			if err == nil {
				t.Fatal("expected capture error")
			}
			want := []string{"printf", "a b", "$HOME"}
			if mode == "userns-v1" {
				want = append([]string{"/usr/local/bin/workspace-exec", "/usr/bin/env", "--chdir=/run/workspace/doc/probe", "--"}, want...)
			}
			wantDir := "/run/workspace/doc/probe"
			if mode == "userns-v1" {
				wantDir = "/"
			}
			if !reflect.DeepEqual(got.Cmd, want) || got.User != "user" || got.WorkingDir != wantDir {
				t.Fatalf("exec: %+v", got)
			}
		})
	}
}

func TestDesktopExecRejectsRoot(t *testing.T) {
	_, err := (&Docker{}).Exec(context.Background(), "desktop", engine.ExecSpec{DesktopSession: true, User: "root"})
	if err == nil {
		t.Fatalf("error: %v", err)
	}
}
