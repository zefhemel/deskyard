package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failingBody struct {
	head io.Reader
}

func (f *failingBody) Read(p []byte) (int, error) {
	n, err := f.head.Read(p)
	if err == io.EOF {
		time.Sleep(100 * time.Millisecond)
		return n, errors.New("client went away")
	}
	return n, err
}

func upload(t *testing.T, c *http.Client, base, name string, body io.Reader) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/api/desktops/alice/upload?name="+url.QueryEscape(name), body)
	req.Header.Set("Origin", base)
	resp, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func desktopFiles(t *testing.T, root string) []string {
	t.Helper()
	ents, _ := os.ReadDir(filepath.Join(root, "Desktop"))
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func TestUploadLandsInDesktop(t *testing.T) {
	_, ts, root := newFilesServer(t)
	admin := login(t, ts, "admin")
	big := bytes.Repeat([]byte("0123456789abcdef"), 200000)
	for name, data := range map[string][]byte{"a.txt": []byte("alpha"), "b.bin": big} {
		code, body := upload(t, admin, ts.URL, name, bytes.NewReader(data))
		if code != 200 || !strings.Contains(body, `"/workspace/Desktop/`+name+`"`) {
			t.Fatalf("%s: %d %s", name, code, body)
		}
		got, err := os.ReadFile(filepath.Join(root, "Desktop", name))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s content differs (%v)", name, err)
		}
	}
	// A clash gets (2), (3) before the extension; a name without one just the suffix.
	for _, want := range []string{"a (2).txt", "a (3).txt"} {
		code, body := upload(t, admin, ts.URL, "a.txt", strings.NewReader("again"))
		if code != 200 || !strings.Contains(body, `"/workspace/Desktop/`+want+`"`) {
			t.Fatalf("clash: %d %s", code, body)
		}
	}
	upload(t, admin, ts.URL, "README", strings.NewReader("1"))
	if _, body := upload(t, admin, ts.URL, "README", strings.NewReader("2")); !strings.Contains(body, "README (2)") {
		t.Errorf("no extension: %s", body)
	}
	for _, n := range desktopFiles(t, root) {
		if strings.HasSuffix(n, ".part") {
			t.Errorf("left %s", n)
		}
	}
	if code, body := upload(t, admin, ts.URL, "empty", strings.NewReader("")); code != 200 || !strings.Contains(body, `"bytes":0`) {
		t.Errorf("empty file: %d %s", code, body)
	}
}

func TestUploadRefusesBadNames(t *testing.T) {
	_, ts, root := newFilesServer(t)
	admin := login(t, ts, "admin")
	for _, n := range []string{"", "a/b", "../x", "..", ".", "a\x00b", strings.Repeat("x", 300), "/etc/passwd"} {
		if code, body := upload(t, admin, ts.URL, n, strings.NewReader("x")); code != 400 {
			t.Errorf("%q: %d %s", n, code, body)
		}
	}
	if got := desktopFiles(t, root); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestUploadCap(t *testing.T) {
	old := maxUpload
	maxUpload = 1000
	t.Cleanup(func() { maxUpload = old })
	_, ts, root := newFilesServer(t)
	admin := login(t, ts, "admin")
	if code, _ := upload(t, admin, ts.URL, "ok", bytes.NewReader(make([]byte, 1000))); code != 200 {
		t.Errorf("exactly the cap: %d", code)
	}
	// Declared too big: refused up front.
	if code, _ := upload(t, admin, ts.URL, "big", bytes.NewReader(make([]byte, 1001))); code != 413 {
		t.Errorf("declared: %d", code)
	}
	// Undeclared (chunked) size too big: counted while streaming.
	if code, _ := upload(t, admin, ts.URL, "chunked", io.MultiReader(bytes.NewReader(make([]byte, 600)), bytes.NewReader(make([]byte, 600)))); code != 413 {
		t.Errorf("streamed: %d", code)
	}
	if got := desktopFiles(t, root); len(got) != 1 || got[0] != "ok" {
		t.Errorf("only ok should remain, got %v", got)
	}
}

func TestUploadAbortLeavesNothing(t *testing.T) {
	_, ts, root := newFilesServer(t)
	admin := login(t, ts, "admin")
	upload(t, admin, ts.URL, "seed", strings.NewReader("s")) // creates ~/Desktop
	code, _ := upload(t, admin, ts.URL, "partial", &failingBody{head: bytes.NewReader(make([]byte, 50000))})
	if code == 200 {
		t.Fatal("an aborted upload succeeded")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(desktopFiles(t, root)) != 1 {
		time.Sleep(50 * time.Millisecond)
	}
	if got := desktopFiles(t, root); len(got) != 1 || got[0] != "seed" {
		t.Errorf("after the abort: %v", got)
	}
}

func TestUploadAccess(t *testing.T) {
	s, ts, root := newFilesServer(t)
	if code, _ := upload(t, login(t, ts, "ben"), ts.URL, "v.txt", strings.NewReader("x")); code != 200 {
		t.Errorf("a viewer: %d", code)
	}
	if code, _ := upload(t, login(t, ts, "anna"), ts.URL, "n.txt", strings.NewReader("x")); code != 404 {
		t.Errorf("no access: %d", code)
	}
	if code, _ := upload(t, &http.Client{}, ts.URL, "n.txt", strings.NewReader("x")); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	_, secret, _ := s.tokens.create("bot", "id-alice", "admin", nil)
	req, _ := http.NewRequest("POST", ts.URL+"/api/desktops/alice/upload?name=t.txt", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("agent token: %d", resp.StatusCode)
	}
	// Same-origin check: a state change from another origin is refused.
	req, _ = http.NewRequest("POST", ts.URL+"/api/desktops/alice/upload?name=t.txt", strings.NewReader("x"))
	req.Header.Set("Origin", "https://evil.example")
	resp, err = login(t, ts, "admin").Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("cross-origin: %d", resp.StatusCode)
	}
	if got := desktopFiles(t, root); len(got) != 1 || got[0] != "v.txt" {
		t.Errorf("files %v", got)
	}
}

func TestUploadStoppedDesktop(t *testing.T) {
	_, ts := newTestServer(t) // alice is stopped
	if code, _ := upload(t, login(t, ts, "admin"), ts.URL, "x", strings.NewReader("x")); code != 409 {
		t.Errorf("stopped: %d", code)
	}
}
