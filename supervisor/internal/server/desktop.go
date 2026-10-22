package server

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
)

// serveDesktop handles /<name>/…: the desktop page (the UI), the KasmVNC
// client files taken from the desktop's image, and the desktop WebSocket.
// Nothing else from a desktop is ever served on this origin.
func (s *Server) serveDesktop(w http.ResponseWriter, r *http.Request, name, rest string) {
	r, ok := s.withSession(w, r)
	if !ok {
		if rest == "websockify" {
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	a, _ := s.account(r)
	c, err := s.Desktops.Get(r.Context(), name)
	if err != nil || !auth.Can(a, auth.View, c.ID) {
		http.NotFound(w, r) // the same for missing and invisible desktops
		return
	}
	switch {
	case !strings.HasPrefix(r.URL.Path, "/"+name+"/"):
		http.Redirect(w, r, "/"+name+"/", http.StatusFound)
	case rest == "":
		s.writeIndex(w) // the UI's desktop page
	case rest == "websockify":
		s.proxyDesktop(w, r, c)
	case strings.HasPrefix(rest, "vnc/"):
		s.serveClientFile(w, r, c, strings.TrimPrefix(rest, "vnc/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveClientFile(w http.ResponseWriter, r *http.Request, c desktops.Desktop, file string) {
	if c.ImageID == "" {
		http.NotFound(w, r)
		return
	}
	dir, err := s.clients.dirFor(r.Context(), c.ImageID)
	if err != nil {
		s.Log.Warn("extracting the web client", "image", c.ImageID, "err", err)
		writeError(w, http.StatusBadGateway, "the desktop client isn't available")
		return
	}
	clean := cleanClientPath(file)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, filepath.Join(dir, filepath.FromSlash(clean)))
}

// cleanClientPath keeps a requested client file inside the client's folder.
func cleanClientPath(file string) string {
	clean := path.Clean("/" + file)
	if clean == "/" {
		return "/vnc.html"
	}
	return clean
}

// proxyDesktop proxies the desktop WebSocket to the desktop, adding the
// desktop's token and dropping the session cookie, and tracks who's viewing.
func (s *Server) proxyDesktop(w http.ResponseWriter, r *http.Request, c desktops.Desktop) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin WebSocket refused")
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeError(w, http.StatusBadRequest, "WebSocket expected")
		return
	}
	addr, token, err := s.Desktops.Target(r.Context(), c.Name)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	a, _ := s.account(r)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	cn := &conn{username: a.Username, desktopID: c.ID, cancel: cancel}
	s.viewers.add(c.Name, cn)
	s.publishViewers(c.Name, c.ID)
	defer func() {
		s.viewers.remove(c.Name, cn)
		s.publishViewers(c.Name, c.ID)
	}()
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = addr
			pr.Out.URL.Path = "/websockify"
			pr.Out.URL.RawQuery = ""
			pr.Out.Host = addr
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Set("X-Deskyard-Token", token)
		},
		ErrorLog: nil,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				s.Log.Debug("desktop proxy", "desktop", c.Name, "err", err)
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r.WithContext(ctx))
}

// clientFiles extracts KasmVNC's web client from desktop images (never from
// running containers, which their agents control), once per image ID.
type clientFiles struct {
	eng interface {
		CopyFromImage(ctx context.Context, ref, path string) (io.ReadCloser, error)
	}
	dir string
	mu  sync.Mutex
}

const clientPath = "/usr/share/kasmvnc/www"

func (cf *clientFiles) dirFor(ctx context.Context, imageID string) (string, error) {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	key := strings.TrimPrefix(imageID, "sha256:")
	if key == "" || strings.ContainsAny(key, "/\\.") {
		return "", errors.New("bad image ID")
	}
	dir := filepath.Join(cf.dir, key)
	if _, err := os.Stat(filepath.Join(dir, "vnc.html")); err == nil {
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
