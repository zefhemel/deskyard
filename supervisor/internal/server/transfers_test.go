package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// revocations end a running transfer of ben's (full on alice).
var transferRevocations = map[string]func(t *testing.T, s *Server, cred string){
	"access removed": func(t *testing.T, s *Server, _ string) {
		if err := s.Accounts.SetAccess("alice", map[string]auth.Level{}); err != nil {
			t.Fatal(err)
		}
	},
	"lowered to observe": func(t *testing.T, s *Server, _ string) {
		if err := s.Accounts.SetAccess("alice", map[string]auth.Level{"ben": auth.LevelObserve}); err != nil {
			t.Fatal(err)
		}
	},
	"account deleted": func(t *testing.T, s *Server, _ string) {
		if err := s.Accounts.Delete("ben"); err != nil {
			t.Fatal(err)
		}
	},
	"credential deleted": func(t *testing.T, s *Server, cred string) {
		if err := s.credentials.remove(func(c credential) bool { return c.ID == cred }); err != nil {
			t.Fatal(err)
		}
	},
}

// transferClient is how ben makes requests: with a client credential for
// "credential deleted", else signed in in a browser.
func transferClient(t *testing.T, s *Server, ts *httptest.Server, how string) (do func(*http.Request) (*http.Response, error), cred string) {
	if how == "credential deleted" {
		c, bearer := issueCredential(t, s, accountNamed(t, s, "ben"))
		return func(req *http.Request) (*http.Response, error) {
			req.Header.Set("Authorization", "Bearer "+bearer)
			return http.DefaultClient.Do(req)
		}, c.ID
	}
	return login(t, ts, "ben").Do, ""
}

// A download that is running ends when its access does: it doesn't stream
// the rest of the file.
func TestRevocationEndsRunningUpload(t *testing.T) {
	for how, revoke := range transferRevocations {
		t.Run(how, func(t *testing.T) {
			s, ts, root := newFilesServer(t)
			do, cred := transferClient(t, s, ts, how)
			body, feed := io.Pipe()
			defer feed.Close()
			req, _ := http.NewRequest("POST", ts.URL+"/api/computers/alice/upload?name=late.txt", body)
			req.Header.Set("Origin", ts.URL)
			req.ContentLength = -1
			done := make(chan int, 1)
			go func() {
				resp, err := do(req)
				if err != nil {
					done <- 0
					return
				}
				resp.Body.Close()
				done <- resp.StatusCode
			}()
			if _, err := feed.Write(bytes.Repeat([]byte("x"), 64<<10)); err != nil {
				t.Fatal(err)
			}
			revoke(t, s, cred)
			// The client keeps the upload open: it ends on the server's side.
			go func() { _, _ = io.Copy(feed, zeroes{}) }()
			select {
			case code := <-done:
				if code == 200 {
					t.Errorf("the upload succeeded after %s", how)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("the upload went on after %s", how)
			}
			feed.Close()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) && len(desktopFiles(t, root)) != 0 {
				time.Sleep(50 * time.Millisecond)
			}
			if got := desktopFiles(t, root); len(got) != 0 {
				t.Errorf("after %s: %v", how, got)
			}
		})
	}
}

type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) {
	time.Sleep(time.Millisecond)
	clear(p)
	return len(p), nil
}
