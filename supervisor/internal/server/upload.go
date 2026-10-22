package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// Dropping files onto a desktop's screen: one POST per file, the raw file as
// the body. The bytes stream into the desktop as `user`, into a hidden
// ".part" file in ~/Desktop, and are renamed to their final name only when
// complete, so a half-written file never shows up on the desktop.

const (
	uploadDir     = "/workspace/Desktop"
	maxUploadName = 200 // bytes; the .part name adds a dot, a random part and ".part"
)

// maxUpload is the biggest file accepted (a variable so tests can lower it).
var maxUpload int64 = 2 << 30

var errTooBig = errors.New("the file is over the 2 GB limit")

// uploadName validates a client's file name. Names are kept as given (a
// leading dot is fine: .bashrc is a legitimate upload), but a path, NUL, "."
// and ".." are refused: the name is one file name in ~/Desktop, nothing more.
func uploadName(n string) error {
	switch {
	case n == "" || n == "." || n == "..":
		return errors.New("a file name is required")
	case strings.ContainsAny(n, "/\x00"):
		return errors.New("the file name can't contain / or NUL")
	case len(n) > maxUploadName:
		return errors.New("the file name is too long")
	}
	return nil
}

// Moves the finished .part ($2) into $1 as $3$4, or "$3 (2)$4", "$3 (3)$4"…
// when that name is taken by anything (a file, a folder, a link: a plain ln
// into an existing folder would drop the file inside it). ln itself still
// refuses an existing name, which guards the race between two uploads; that
// refusal is the only failure retried, any other ln error ends the upload.
// Prints the final name.
const finishUploadScript = `d=$1; part=$2; base=$3; ext=$4
taken() { [ -e "$d/$1" ] || [ -L "$d/$1" ]; }
n=1; final="$base$ext"
while :; do
  if ! taken "$final"; then
    err=$(ln "$part" "$d/$final" 2>&1) && break
    if ! taken "$final"; then echo "$err" >&2; rm -f "$part"; exit 1; fi
  fi
  n=$((n+1))
  [ "$n" -gt 9999 ] && { rm -f "$part"; echo "no free name for $base$ext" >&2; exit 1; }
  final="$base ($n)$ext"
done
rm -f "$part"
printf %s "$final"`

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, desktops.ErrStopped.Error())
		return
	}
	name := r.URL.Query().Get("name")
	if err := uploadName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.ContentLength > maxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, errTooBig.Error())
		return
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	part := uploadDir + "/." + name + "." + hex.EncodeToString(rnd[:]) + ".part"

	n, err := s.streamIn(r.Context(), c, part, r.Body)
	if err != nil {
		s.dropPart(c, part)
		switch {
		case errors.Is(err, errTooBig):
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		case r.Context().Err() != nil:
			s.Log.Info("upload abandoned", "desktop", c.Name, "name", name)
		default:
			writeError(w, http.StatusBadGateway, "upload failed: "+err.Error())
		}
		return
	}
	// The rest runs even if the client leaves now, so no .part is left behind.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	ext := path.Ext(name)
	if ext == name { // ".bashrc": a dotfile, not an extension
		ext = ""
	}
	var final bytes.Buffer
	err = s.execStream(ctx, c, 0, []string{"sh", "-c", finishUploadScript, "sh", uploadDir, part, strings.TrimSuffix(name, ext), ext},
		func(out io.Reader) error { _, err := io.Copy(&final, out); return err })
	if err != nil {
		s.dropPart(c, part)
		writeError(w, http.StatusBadGateway, "upload failed: "+err.Error())
		return
	}
	s.Log.Info("upload", "desktop", c.Name, "name", final.String(), "bytes", n)
	writeJSON(w, http.StatusOK, map[string]any{"path": uploadDir + "/" + final.String(), "bytes": n})
}

// streamIn copies body into the file at part (creating ~/Desktop first),
// counting against the cap.
func (s *Server) streamIn(ctx context.Context, c desktops.Desktop, part string, body io.Reader) (int64, error) {
	p, err := s.Eng.Exec(ctx, c.Container.ID, engine.ExecSpec{
		Cmd:  []string{"sh", "-c", `mkdir -p "$1" && cat > "$2"`, "sh", uploadDir, part},
		User: "user", WorkDir: filesRoot, Env: append([]string{"LC_ALL=C"}, execEnv...), Stdin: true,
	})
	if err != nil {
		return 0, err
	}
	defer p.Close()
	var stderr bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		var wg = make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, p.Stdout)
			close(wg)
		}()
		if p.Stderr != nil {
			_, _ = io.Copy(&limitedWriter{w: &stderr, n: 4096}, p.Stderr)
		}
		<-wg
	}()
	n, cerr := copyCapped(p.Stdin, body, maxUpload)
	_ = p.Stdin.Close()
	if cerr != nil {
		p.Close()
		<-drained
		return n, cerr
	}
	<-drained
	code, werr := p.Wait()
	if werr != nil {
		return n, werr
	}
	if code != 0 {
		return n, &execError{code, strings.TrimSpace(stderr.String())}
	}
	return n, nil
}

// copyCapped copies src to dst, failing with errTooBig once more than limit bytes arrive.
func copyCapped(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	var total int64
	buf := make([]byte, 256<<10)
	for {
		k, rerr := src.Read(buf)
		if k > 0 {
			total += int64(k)
			if total > limit {
				return total, errTooBig
			}
			if _, err := dst.Write(buf[:k]); err != nil {
				return total, err
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

// dropPart deletes an unfinished upload, whatever happened to the request.
func (s *Server) dropPart(c desktops.Desktop, part string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.execOutput(ctx, c, "rm", "-f", "--", part); err != nil {
		s.Log.Warn("couldn't remove an unfinished upload", "desktop", c.Name, "part", part, "err", err)
	}
}
