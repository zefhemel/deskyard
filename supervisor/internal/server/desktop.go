package server

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/web"
)

// serveDesktop handles /<name>/…: the desktop page (the UI), and under
// /<name>/screen/ Selkies' web client taken from the desktop's image, our
// additions to it, and the desktop's stream. Nothing else from a desktop is
// ever served on this origin.
func (s *Server) serveDesktop(w http.ResponseWriter, r *http.Request, name, rest string) {
	r, ok := s.withSession(w, r)
	if !ok {
		if strings.HasPrefix(rest, "screen/api/") {
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	a, _ := s.account(r)
	c, err := s.Environments.Get(r.Context(), name)
	if err != nil || !auth.Can(a, auth.View, c.ID) {
		http.NotFound(w, r) // the same for missing and invisible desktops
		return
	}
	switch {
	case !strings.HasPrefix(r.URL.Path, "/"+name+"/"):
		http.Redirect(w, r, "/"+name+"/", http.StatusFound)
	case rest == "":
		s.writeIndex(w) // the UI's environment page, with its desktop and terminal tabs
	case rest == "screen":
		http.Redirect(w, r, "/"+name+"/screen/", http.StatusFound)
	case strings.HasPrefix(rest, "screen/"):
		s.serveScreen(w, r, c, strings.TrimPrefix(rest, "screen/"))
	default:
		http.NotFound(w, r)
	}
}

// serveScreen serves /<name>/screen/<file>. Of Selkies' HTTP API only the
// stream is proxied; its other endpoints (upload, files, screenshots, …) are
// refused here as well as by the desktop's Caddy.
func (s *Server) serveScreen(w http.ResponseWriter, r *http.Request, c environments.Environment, file string) {
	switch {
	case file == "api/websockets" || file == "api/websockets/":
		s.serveStream(w, r, c)
		return
	case file == "api" || strings.HasPrefix(file, "api/"):
		writeError(w, http.StatusNotFound, "not served")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch clean := cleanClientPath(file); {
	case (clean == "/" || clean == "/index.html") && file != "" && file != "index.html":
		// Any other spelling of the page would serve it without our additions.
		http.Redirect(w, r, "/"+c.Name+"/screen/", http.StatusFound)
		return
	}
	switch file {
	case "deskyard-v1.js":
		data, err := web.Dist.ReadFile("dist/deskyard-screen.js")
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "desktop adapter is missing; rebuild the supervisor web UI")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(data)
	case "deskyard.js":
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(screenAdditions)
	case "", "index.html":
		s.serveScreenPage(w, r, c)
	default:
		s.serveClientFile(w, r, c, file)
	}
}

//go:embed screen_additions.js
var screenAdditions []byte

// additionsTag loads our additions before any of Selkies' own scripts.
const additionsTag = `<script src="deskyard.js"></script>`

const modernAdditionsTag = `<script type="module" src="deskyard-v1.js"></script>`

// Images predating the hook contract keep the original bridge while they are
// upgraded. An unknown explicit contract must never silently lose controls.
func screenAdapterTag(page []byte) (string, error) {
	if !bytes.Contains(page, []byte(`name="deskyard-client-contract"`)) {
		return additionsTag, nil
	}
	if bytes.Contains(page, []byte(`<meta name="deskyard-client-contract" content="1">`)) {
		return modernAdditionsTag, nil
	}
	return "", errors.New("this desktop client needs a newer supervisor; upgrade the supervisor or select a compatible desktop image")
}

// serveScreenPage serves the client's index.html with our additions.
func (s *Server) serveScreenPage(w http.ResponseWriter, r *http.Request, c environments.Environment) {
	dir, ok := s.clientDir(w, r, c)
	if !ok {
		return
	}
	page, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "the desktop client isn't available")
		return
	}
	tag, err := screenAdapterTag(page)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if i := bytes.Index(page, []byte("<head>")); i >= 0 {
		i += len("<head>")
		page = slices.Concat(page[:i:i], []byte(tag), page[i:])
	} else {
		page = slices.Concat([]byte(tag), page)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (s *Server) clientDir(w http.ResponseWriter, r *http.Request, c environments.Environment) (string, bool) {
	if c.ImageID == "" {
		http.NotFound(w, r)
		return "", false
	}
	dir, err := s.clients.dirFor(r.Context(), c.ImageID)
	if err != nil {
		s.Log.Warn("extracting the web client", "image", c.ImageID, "err", err)
		writeError(w, http.StatusBadGateway, "the desktop client isn't available")
		return "", false
	}
	return dir, true
}

func (s *Server) serveClientFile(w http.ResponseWriter, r *http.Request, c environments.Environment, file string) {
	dir, ok := s.clientDir(w, r, c)
	if !ok {
		return
	}
	p := filepath.Join(dir, filepath.FromSlash(cleanClientPath(file)))
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		http.NotFound(w, r) // no folder listings
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, p)
}

// cleanClientPath keeps a requested client file inside the client's folder.
func cleanClientPath(file string) string {
	return path.Clean("/" + file)
}

// streamOrigin is the Origin the stream reaches Selkies with, after the
// supervisor's own same-origin check: the image allows only this one
// (SELKIES_ALLOWED_ORIGINS), and .invalid is never a real site's.
const streamOrigin = "http://supervisor.deskyard.invalid"

// serveStream proxies the desktop's stream (Selkies' WebSocket) to the
// desktop, adding the desktop's token and dropping the session cookie, and
// tracks who's viewing. An observer's messages pass observeFilter: they watch,
// never act on the desktop. ?tab= names the page's browser tab, for its mic
// badge. A plain GET is the client's reconnect probe.
func (s *Server) serveStream(w http.ResponseWriter, r *http.Request, c environments.Environment) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.probeStream(w, r, c)
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin WebSocket refused")
		return
	}
	tab := r.URL.Query().Get("tab")
	if !validTab(tab) {
		writeError(w, http.StatusBadRequest, "bad tab id")
		return
	}
	addr, token, err := s.Environments.Target(r.Context(), c.Name)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	release, err := s.streamDials.wait(r.Context(), c.Name)
	if err != nil {
		return // the client went away while waiting its turn
	}
	defer release()
	a, _ := s.account(r)
	observer := !auth.Can(a, auth.View, c.ID) // allowed to watch, not to use
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	cn := &conn{username: a.Username, tab: tab, environment: c.ID, cancel: cancel}
	s.viewers.add(c.Name, cn)
	s.publishViewers(c.Name, c.ID)
	defer func() {
		s.viewers.remove(c.Name, cn)
		s.publishViewers(c.Name, c.ID)
	}()
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) { streamRequest(pr.Out, addr, token) },
		ModifyResponse: func(resp *http.Response) error {
			release() // Selkies has answered: the next dial's wait starts now
			if conn, ok := resp.Body.(io.ReadWriteCloser); ok && resp.StatusCode == http.StatusSwitchingProtocols {
				if observer {
					conn = newObserveFilter(conn) // under the watch, whose START_VIDEO it lets through
				}
				// Selkies may have stopped the desktop's stream: restarted
				// if this viewer gets no picture.
				resp.Body = newStreamWatch(conn, streamPictureWait, func() {
					s.Log.Info("desktop stream: no picture for a new viewer; restarting it", "environment", c.Name, "account", a.Username)
				})
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			release()
			if !errors.Is(err, context.Canceled) {
				s.Log.Debug("desktop stream", "environment", c.Name, "err", err)
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r.WithContext(ctx))
}

// streamDialGap is the least time between Selkies' answer to one stream dial
// of a desktop and the next dial. Selkies refuses (close 4029) a connection
// within 500 ms of the previous one from the same address, and every viewer
// reaches it from the same address: the desktop's Caddy.
const streamDialGap = 600 * time.Millisecond

// dialGates spaces out the stream dials of each desktop by streamDialGap, so
// viewers opening a desktop together, or reconnecting together after a
// restart, all get in (each waits up to streamDialGap per viewer ahead).
type dialGates struct {
	mu    sync.Mutex
	gates map[string]*dialGate
}

type dialGate struct {
	turn chan struct{} // holds one token while a dial is under way
	last time.Time     // when the previous dial was answered; written by the turn's holder
}

func newDialGates() *dialGates { return &dialGates{gates: map[string]*dialGate{}} }

// wait takes the desktop's turn to dial, streamDialGap after the previous
// dial's answer. release, once the answer arrived (or the dial failed), hands
// the turn on; calling it again does nothing.
func (g *dialGates) wait(ctx context.Context, name string) (release func(), err error) {
	g.mu.Lock()
	d := g.gates[name]
	if d == nil {
		d = &dialGate{turn: make(chan struct{}, 1)}
		g.gates[name] = d
	}
	g.mu.Unlock()
	select {
	case d.turn <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if wait := time.Until(d.last.Add(streamDialGap)); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			<-d.turn
			return nil, ctx.Err()
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			d.last = time.Now()
			<-d.turn
		})
	}, nil
}

// streamRequest points a request at the desktop's stream endpoint.
func streamRequest(out *http.Request, addr, token string) {
	out.URL.Scheme = "http"
	out.URL.Host = addr
	out.URL.Path = "/api/websockets"
	out.URL.RawPath = ""
	out.URL.RawQuery = ""
	out.Host = addr
	out.Header.Del("Cookie")
	out.Header.Del("Authorization")
	out.Header.Set("Origin", streamOrigin)
	out.Header.Set("X-Deskyard-Token", token)
}

// probeStream answers the client's reconnect probe: 204 when Selkies is up,
// otherwise 503, which makes the client wait and probe again. (Never 409:
// Selkies' client takes that as "the server serves WebRTC" and switches.)
func (s *Server) probeStream(w http.ResponseWriter, r *http.Request, c environments.Environment) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	addr, token, err := s.Environments.Target(r.Context(), c.Name)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr, nil)
	streamRequest(req, addr, token)
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validTab reports whether a page's tab id is acceptable (it may be empty).
func validTab(tab string) bool {
	if len(tab) > 64 {
		return false
	}
	for _, r := range tab {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// handleMic records whether the mic of one of the account's streams (the one
// from that browser tab) is on, for everyone's badges. The badge goes with the
// stream. The page numbers its reports (seq), so a late one never undoes a
// newer one.
func (s *Server) handleMic(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.environmentFor(w, r, auth.View)
	if !ok {
		return
	}
	var req struct {
		On  bool   `json:"on"`
		Tab string `json:"tab"`
		Seq int64  `json:"seq"`
	}
	if err := readJSON(r, &req); err != nil || req.Tab == "" || !validTab(req.Tab) || req.Seq < 0 {
		writeError(w, http.StatusBadRequest, `{"on": bool, "tab": id, "seq": number} expected`)
		return
	}
	if !s.viewers.setMic(c.Name, a.Username, req.Tab, req.On, req.Seq) {
		writeError(w, http.StatusConflict, "no stream from that tab")
		return
	}
	s.publishViewers(c.Name, c.ID)
	w.WriteHeader(http.StatusNoContent)
}

// clientFiles extracts Selkies' web client from desktop images (never from
// running containers, which their agents control), once per image ID.
type clientFiles struct {
	eng interface {
		CopyFromImage(ctx context.Context, ref, path string) (io.ReadCloser, error)
	}
	dir string
	mu  sync.Mutex
}

// clientPath is where a contract-3 image keeps Selkies' web client.
const clientPath = "/opt/selkies/lib/python3.13/site-packages/selkies/selkies_web"

func (cf *clientFiles) dirFor(ctx context.Context, imageID string) (string, error) {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	key := strings.TrimPrefix(imageID, "sha256:")
	if key == "" || strings.ContainsAny(key, "/\\.") {
		return "", errors.New("bad image ID")
	}
	dir := filepath.Join(cf.dir, key)
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
		return dir, nil
	}
	rc, err := cf.eng.CopyFromImage(ctx, imageID, clientPath)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.MkdirTemp(cf.dir+"-tmp", key+"-")
	if err != nil {
		if err := os.MkdirAll(cf.dir+"-tmp", 0o700); err != nil {
			return "", err
		}
		if tmp, err = os.MkdirTemp(cf.dir+"-tmp", key+"-"); err != nil {
			return "", err
		}
	}
	defer os.RemoveAll(tmp)
	if err := untar(rc, tmp, path.Base(clientPath)); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cf.dir, 0o700); err != nil {
		return "", err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// untar writes the regular files and directories under prefix/ of a tar
// stream into dir (symlinks and anything escaping dir are skipped).
func untar(r io.Reader, dir, prefix string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := strings.CutPrefix(path.Clean(h.Name), prefix)
		if !ok {
			continue
		}
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return err
			}
		}
	}
}
