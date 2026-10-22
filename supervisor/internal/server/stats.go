package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
)

const (
	statsInterval = 5 * time.Second
	statsWindow   = 15 * time.Minute
	statsLinger   = 3 * time.Minute // keep collecting after the last UI page closes
)

type sample struct {
	T          time.Time `json:"t"`
	CPUPercent float64   `json:"cpuPercent"`
	MemBytes   uint64    `json:"memBytes"`
}

// statsCollector keeps a stats stream per running desktop while any UI
// page is open (and a few minutes after), with 15 minutes of history at 5 s.
type statsCollector struct {
	s       *Server
	mu      sync.Mutex
	history map[string][]sample
	last    map[string]statsJSON
	running map[string]context.CancelFunc
	wakeC   chan struct{}
	lastUse time.Time
}

func newStatsCollector(s *Server) *statsCollector {
	return &statsCollector{s: s, history: map[string][]sample{}, last: map[string]statsJSON{},
		running: map[string]context.CancelFunc{}, wakeC: make(chan struct{}, 1)}
}

func (sc *statsCollector) wake() {
	select {
	case sc.wakeC <- struct{}{}:
	default:
	}
}

func (sc *statsCollector) latest(name string) (statsJSON, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	st, ok := sc.last[name]
	return st, ok
}

func (sc *statsCollector) historyOf(name string) []sample {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return append([]sample{}, sc.history[name]...)
}

func (sc *statsCollector) run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		sc.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-sc.wakeC:
		}
	}
}

// reconcile starts streams for running desktops and stops the others.
func (sc *statsCollector) reconcile(ctx context.Context) {
	if sc.s.events.count() > 0 {
		sc.lastUse = time.Now()
	}
	wanted := map[string]desktops.Desktop{}
	if time.Since(sc.lastUse) < statsLinger {
		list, err := sc.s.Desktops.List(ctx)
		if err != nil {
			return
		}
		for _, c := range list {
			if c.State == "running" {
				wanted[c.Name] = c
			}
		}
	}
	host, _ := sc.s.Eng.Info(ctx)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for name, cancel := range sc.running {
		if _, ok := wanted[name]; !ok {
			cancel()
			delete(sc.running, name)
			delete(sc.last, name)
		}
	}
	for name, c := range wanted {
		if _, ok := sc.running[name]; ok {
			continue
		}
		cctx, cancel := context.WithCancel(ctx)
		sc.running[name] = cancel
		go sc.follow(cctx, c, host.CPUs, uint64(host.MemBytes))
	}
}

func (sc *statsCollector) follow(ctx context.Context, c desktops.Desktop, hostCPUs int, hostMem uint64) {
	defer func() {
		sc.mu.Lock()
		if cancel, ok := sc.running[c.Name]; ok {
			cancel()
			delete(sc.running, c.Name)
		}
		sc.mu.Unlock()
	}()
	ch, err := sc.s.Eng.Stats(ctx, c.Container.ID)
	if err != nil {
		return
	}
	var lastSent time.Time
	for st := range ch {
		if st.Time.Sub(lastSent) < statsInterval-500*time.Millisecond {
			continue
		}
		lastSent = st.Time
		// CPU relative to the desktop's limit, or to all host CPUs.
		cpus := float64(hostCPUs)
		if c.Settings.NanoCPUs > 0 {
			cpus = float64(c.Settings.NanoCPUs) / 1e9
		}
		if cpus <= 0 {
			cpus = float64(max(st.OnlineCPUs, 1))
		}
		cpu := min(st.CPUPercent/cpus, 100)
		limit := st.MemLimit
		if c.Settings.MemoryBytes > 0 {
			limit = uint64(c.Settings.MemoryBytes)
		} else if hostMem > 0 && (limit == 0 || limit > hostMem) {
			limit = hostMem
		}
		js := statsJSON{CPUPercent: round1(cpu), MemBytes: st.MemBytes, MemLimit: limit}
		sc.mu.Lock()
		sc.last[c.Name] = js
		h := append(sc.history[c.Name], sample{T: st.Time.UTC(), CPUPercent: js.CPUPercent, MemBytes: js.MemBytes})
		for len(h) > 0 && st.Time.Sub(h[0].T) > statsWindow {
			h = h[1:]
		}
		sc.history[c.Name] = h
		sc.mu.Unlock()
		sc.s.events.publish(event{kind: "stats", desktopID: c.ID, data: map[string]any{
			"name": c.Name, "cpuPercent": js.CPUPercent, "memBytes": js.MemBytes, "memLimit": js.MemLimit, "t": st.Time.UTC(),
		}})
	}
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	s.stats.lastUse = time.Now()
	s.stats.wake()
	limit := uint64(0)
	if st, ok := s.stats.latest(c.Name); ok {
		limit = st.MemLimit
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": s.stats.historyOf(c.Name), "memLimit": limit})
}

// --- screen thumbnails ---------------------------------------------------------------

type thumb struct {
	png  []byte
	at   time.Time
	full bool
}

type thumbnails struct {
	s  *Server
	mu sync.Mutex
	m  map[string]thumb
}

func newThumbnails(s *Server) *thumbnails { return &thumbnails{s: s, m: map[string]thumb{}} }

const thumbWidth = 480

func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, desktops.ErrStopped.Error())
		return
	}
	full := r.URL.Query().Get("full") == "1"
	key := c.Name
	if full {
		key += "/full"
	}
	s.thumbs.mu.Lock()
	t, ok := s.thumbs.m[key]
	s.thumbs.mu.Unlock()
	if !ok || time.Since(t.at) > 8*time.Second || full {
		ctx, cancel := timeout(r, 20*time.Second)
		defer cancel()
		raw, err := s.execOutput(ctx, c, "deskyard", "screenshot", "-")
		if err != nil {
			writeError(w, http.StatusBadGateway, "screenshot failed: "+err.Error())
			return
		}
		if !full {
			if raw, err = scalePNG(raw, thumbWidth); err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
		}
		t = thumb{png: raw, at: time.Now(), full: full}
		s.thumbs.mu.Lock()
		s.thumbs.m[key] = t
		s.thumbs.mu.Unlock()
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	if full {
		w.Header().Set("Content-Disposition", `attachment; filename="`+c.Name+`-`+time.Now().Format("2006-01-02-150405")+`.png"`)
	}
	_, _ = w.Write(t.png)
}

// scalePNG shrinks a PNG to the given width with a box filter.
func scalePNG(b []byte, width int) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	sb := src.Bounds()
	if sb.Dx() <= width {
		return b, nil
	}
	height := sb.Dy() * width / sb.Dx()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		y0, y1 := sb.Min.Y+y*sb.Dy()/height, sb.Min.Y+(y+1)*sb.Dy()/height
		for x := 0; x < width; x++ {
			x0, x1 := sb.Min.X+x*sb.Dx()/width, sb.Min.X+(x+1)*sb.Dx()/width
			var r, g, bl, n uint32
			for yy := y0; yy < max(y1, y0+1); yy++ {
				for xx := x0; xx < max(x1, x0+1); xx++ {
					cr, cg, cb, _ := src.At(xx, yy).RGBA()
					r, g, bl, n = r+cr, g+cg, bl+cb, n+1
				}
			}
			dst.Set(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
		}
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
