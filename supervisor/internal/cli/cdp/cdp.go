// Package cdp forwards a local DevTools endpoint (127.0.0.1:PORT) to a
// desktop's shared Chromium through the remote access API, so Playwright's
// connectOverCDP("http://127.0.0.1:9222") works as if the browser were local.
package cdp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// Forwarder is an http.Handler that proxies to
// {base}/api/computers/{name}/cdp/….
type Forwarder struct {
	Client *remote.Client
	// Local is the host:port clients use to reach this forwarder
	// ("127.0.0.1:9222"); URLs in /json responses are rewritten to it.
	Local string
	// Log, when set, gets one line per connect/disconnect.
	Log func(format string, args ...any)
}

func (f *Forwarder) logf(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}

// localHost reports whether a Host header names this machine; anything
// else (DNS rebinding from a web page) is refused, as Chromium does.
func localHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (f *Forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !localHost(r.Host) {
		http.Error(w, "Host header is not localhost", http.StatusForbidden)
		return
	}
	if !cleanPath(r.URL.Path) {
		http.Error(w, "path is not clean", http.StatusBadRequest)
		return
	}
	if isWebSocket(r) {
		f.serveWS(w, r)
		return
	}
	f.serveHTTP(w, r)
}

// cleanPath reports whether p (decoded, so %2e and %2f count) has no ".",
// ".." or empty segments. The forwarder holds the account's credential: a
// path that climbs out of /cdp/ would reach other API routes, directly or
// through the supervisor's redirect to the cleaned path.
func cleanPath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	c := path.Clean(p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	return c == p
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func (f *Forwarder) remotePath(r *http.Request) string {
	p := "/cdp" + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		p += "?" + r.URL.RawQuery
	}
	return p
}

func (f *Forwarder) serveHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, f.Client.URL(f.remotePath(r)), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.ContentLength = r.ContentLength
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		f.logf("%s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusConflict {
		err := f.Client.ResponseError(resp)
		f.logf("%v", err)
		http.Error(w, err.Error(), resp.StatusCode)
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/json") && resp.StatusCode == http.StatusOK {
		body = RewriteJSON(body, f.Local)
	}
	for _, k := range []string{"Content-Type", "Cache-Control"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func (f *Forwarder) serveWS(w http.ResponseWriter, r *http.Request) {
	up, resp, err := websocket.Dial(r.Context(), f.Client.WSURL(f.remotePath(r)), &websocket.DialOptions{
		HTTPHeader: f.Client.Header(),
		HTTPClient: f.Client.HTTPClient(),
	})
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			err = f.Client.ResponseError(resp)
			f.logf("%v", err)
			http.Error(w, err.Error(), resp.StatusCode)
			return
		}
		f.logf("connecting %s: %v", r.URL.Path, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer up.CloseNow()
	up.SetReadLimit(-1)

	// The default origin check stays on: a web page in a local browser must
	// not reach the shared browser through this port.
	down, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		f.logf("accepting %s: %v", r.URL.Path, err)
		return
	}
	defer down.CloseNow()
	down.SetReadLimit(-1)

	f.logf("connected %s", r.URL.Path)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	errc := make(chan error, 2)
	go func() { errc <- pump(ctx, down, up) }()
	go func() { errc <- pump(ctx, up, down) }()
	err = <-errc
	cancel()
	status := websocket.CloseStatus(err)
	if status == -1 {
		status = websocket.StatusNormalClosure
	}
	down.Close(status, "")
	up.Close(status, "")
	f.logf("disconnected %s", r.URL.Path)
}

func pump(ctx context.Context, dst, src *websocket.Conn) error {
	for {
		typ, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

// RewriteJSON rewrites DevTools URLs in a /json* response body so they
// point at local (host:port). Bodies that aren't JSON come back unchanged.
func RewriteJSON(body []byte, local string) []byte {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return body
	}
	v = rewriteValue(v, local)
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "   ")
	if err := enc.Encode(v); err != nil {
		return body
	}
	return out.Bytes()
}

func rewriteValue(v any, local string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = rewriteValue(x, local)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = rewriteValue(x, local)
		}
		return t
	case string:
		return RewriteURL(t, local)
	}
	return v
}

var cdpPathRE = regexp.MustCompile(`/api/computers/[^/]+/cdp/`)

// devtoolsTail returns the part of a path from "/devtools/" on, or "".
func devtoolsTail(p string) string {
	if i := strings.Index(p, "/devtools/"); i >= 0 {
		return p[i:]
	}
	return ""
}

// RewriteURL points one DevTools URL at local:
//   - ws(s)://any/…/devtools/X           -> ws://local/devtools/X
//   - …?ws=host/…/devtools/X (or wss=)    -> …?ws=local/devtools/X, with the
//     front end's own path made local when it went through the remote
//     access API.
//
// Other strings are returned unchanged.
func RewriteURL(s, local string) string {
	if strings.HasPrefix(s, "ws://") || strings.HasPrefix(s, "wss://") {
		u, err := url.Parse(s)
		if err != nil {
			return s
		}
		tail := devtoolsTail(u.Path)
		if tail == "" {
			return s
		}
		out := "ws://" + local + tail
		if u.RawQuery != "" {
			out += "?" + u.RawQuery
		}
		return out
	}
	if !strings.Contains(s, "ws=") && !strings.Contains(s, "wss=") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	q := u.Query()
	val, key := q.Get("ws"), "ws"
	if val == "" {
		val, key = q.Get("wss"), "wss"
	}
	if val == "" {
		return s
	}
	tail := devtoolsTail("/" + strings.TrimPrefix(val, "/"))
	if tail == "" {
		return s
	}
	q.Del(key)
	q.Set("ws", local+tail)
	// A front end served through the remote access API
	// (/api/computers/NAME/cdp/devtools/…) is served by the forwarder at
	// /devtools/….
	if loc := cdpPathRE.FindStringIndex(u.Path); loc != nil {
		u.Path = u.Path[loc[1]-1:]
		if u.IsAbs() {
			u.Scheme, u.Host = "http", local
		}
	}
	u.RawQuery = strings.NewReplacer("%2F", "/", "%3A", ":").Replace(q.Encode())
	return u.String()
}

// Probe asks the desktop's CDP for /json/version, so problems (bad credential,
// stopped desktop) show before anything listens. Chromium may need a few
// seconds to start.
func Probe(ctx context.Context, c *remote.Client) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL("/cdp/json/version"), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.ResponseError(resp)
	}
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, fmt.Errorf("%s's browser answered with something that isn't DevTools JSON: %v", c.Label(), err)
	}
	return v, nil
}

// Serve listens on 127.0.0.1:port (0 = any free port) and forwards until ctx
// ends. ready gets the address once listening.
func Serve(ctx context.Context, c *remote.Client, port int, ready func(addr string), logf func(string, ...any)) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") || strings.Contains(err.Error(), "Only one usage") {
			return fmt.Errorf("port %d is in use (another `deskyard browser connect` or a local Chromium?): pass --port N to use another one", port)
		}
		return err
	}
	addr := ln.Addr().String()
	srv := &http.Server{
		Handler:           &Forwarder{Client: c, Local: addr, Log: logf},
		ReadHeaderTimeout: 10 * time.Second,
	}
	if ready != nil {
		ready(addr)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		return nil
	case err := <-errc:
		return err
	}
}
