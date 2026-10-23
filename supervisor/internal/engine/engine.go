// Package engine is the supervisor's own view of a container engine. All
// supervisor logic goes through this interface; only engine/docker knows the
// Docker SDK, so a Podman implementation can be added beside it.
package engine

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// ErrNotFound is returned for a container, network or image that doesn't exist.
var ErrNotFound = errors.New("not found")

type Mount struct {
	Source, Target string // Source is a host path
	ReadOnly       bool
}

type ContainerSpec struct {
	Name, Image string
	Labels      map[string]string
	Env         map[string]string
	Mounts      []Mount
	// NetworkContainer shares this existing container's network namespace.
	// Mutually exclusive with Network, Aliases and PublishLocal.
	NetworkContainer string
	// CapAdd explicitly grants capabilities; desktop defaults remain unchanged.
	CapAdd  []string
	Network string
	Aliases []string
	// PublishLocal, when set, publishes the container's port 8080 on host
	// 127.0.0.1 at this port (0 picks a free one).
	PublishLocal *int
	MemoryBytes  int64
	NanoCPUs     int64
	ShmBytes     int64
	// Flatpak runs the container with the security options Flatpak's sandbox
	// needs (seccomp profile, apparmor and system paths unconfined).
	Flatpak bool
	Restart string // "unless-stopped", "no"
	Cmd     []string
	// Devices are host devices passed in at the same path (e.g. /dev/video0).
	Devices []string
	// Entrypoint replaces the image's entrypoint when set.
	Entrypoint []string
	// NoHealthcheck turns off the image's HEALTHCHECK, which would otherwise
	// run the image's own command in the container.
	NoHealthcheck bool
}

type Container struct {
	ID, Name, Image, ImageID string
	Labels                   map[string]string
	Env                      map[string]string
	State                    string // created, running, paused, restarting, exited, dead
	// Health is empty without an image healthcheck, otherwise starting, healthy or unhealthy.
	// Raw healthcheck logs are deliberately excluded.
	Health           string
	NetworkContainer string // namespace owner, empty for independent networking
	Restart          string // configured engine restart policy
	// Error is why the engine's last start failed; empty after a successful start.
	Error                 string
	StartedAt             time.Time
	IP                    map[string]string // network name → address
	LocalPort             int               // host 127.0.0.1 port published for 8080, 0 if none
	MemoryBytes, NanoCPUs int64
	Devices               []string // host devices passed in (their paths on the host)
}

func (c Container) Running() bool { return c.State == "running" || c.State == "restarting" }

type ExecSpec struct {
	Cmd        []string
	User       string
	WorkDir    string
	Env        []string
	Tty        bool
	Stdin      bool
	Cols, Rows uint
}

// Process is a running exec. With Tty, everything comes on Stdout.
// Close must unblock stream readers/writers and Wait. Exec implementations must
// close attachments on context cancellation and use that context for Wait.
type Process struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	Stderr io.Reader
	Resize func(cols, rows uint) error
	Wait   func() (int, error)
	Close  func()
}

type Stats struct {
	CPUPercent float64 // of one CPU × 100, i.e. 250 = two and a half CPUs busy
	OnlineCPUs int
	MemBytes   uint64
	MemLimit   uint64
	Time       time.Time
}

type Network struct {
	ID, Name, Subnet string
	Labels           map[string]string
}

type Image struct {
	ID     string
	Labels map[string]string
	User   string // the image's USER, "" for root
	Arch   string // e.g. amd64, arm64
}

// BuildSpec describes an image build from a tar build context.
type BuildSpec struct {
	Context    io.Reader // tar archive
	Dockerfile string    // path of the Dockerfile inside Context
	Labels     map[string]string
	NoCache    bool
}

type PullProgress struct {
	Status         string
	Current, Total int64
}

type Event struct {
	ContainerID string
	Name        string // container name
	Action      string // start, die, stop, destroy, create, …
	Labels      map[string]string
}

type HostInfo struct {
	CPUs     int
	MemBytes int64
}

type Engine interface {
	Create(ctx context.Context, spec ContainerSpec) (string, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Restart(ctx context.Context, id string, timeout time.Duration) error
	UpdateRestartPolicy(ctx context.Context, id, policy string) error
	Remove(ctx context.Context, id string) error
	Inspect(ctx context.Context, id string) (Container, error)
	List(ctx context.Context, labels map[string]string) ([]Container, error)
	Exec(ctx context.Context, id string, spec ExecSpec) (*Process, error)
	Top(ctx context.Context, id string, args string) (titles []string, rows [][]string, err error)
	Stats(ctx context.Context, id string) (<-chan Stats, error)
	CreateNetwork(ctx context.Context, name, subnet string, labels map[string]string) error
	RemoveNetwork(ctx context.Context, name string) error
	ConnectNetwork(ctx context.Context, network, container string, aliases []string) error
	DisconnectNetwork(ctx context.Context, network, container string) error
	Networks(ctx context.Context, labels map[string]string) ([]Network, error)
	Pull(ctx context.Context, ref string, progress func(PullProgress)) error
	ImageInspect(ctx context.Context, ref string) (Image, error)
	// CopyFromImage returns a tar stream of path inside the image, read from a
	// never-started container of it.
	CopyFromImage(ctx context.Context, ref, path string) (io.ReadCloser, error)
	// CopyToContainer extracts a tar archive into dir inside a container
	// (created or running).
	CopyToContainer(ctx context.Context, id, dir string, archive io.Reader) error
	// Build builds an image, passing each line of build output to log.
	// Cancelling ctx stops the build.
	Build(ctx context.Context, spec BuildSpec, log func(line string)) (imageID string, err error)
	// RemoveImage returns ErrNotFound if ref doesn't exist, and an error
	// mentioning "in use" if a container uses it.
	RemoveImage(ctx context.Context, ref string) error
	Tag(ctx context.Context, imageID, ref string) error
	// ImageTags lists the tags of a repository ("name:tag" → image ID).
	ImageTags(ctx context.Context, repo string) (map[string]string, error)
	Events(ctx context.Context) (<-chan Event, error)
	Info(ctx context.Context) (HostInfo, error)
}

// Output runs cmd and returns its stdout, stderr and exit code.
func Output(ctx context.Context, e Engine, id string, spec ExecSpec) (stdout, stderr []byte, code int, err error) {
	p, err := e.Exec(ctx, id, spec)
	if err != nil {
		return nil, nil, -1, err
	}
	closeProcess := sync.OnceFunc(p.Close)
	stop := context.AfterFunc(ctx, closeProcess)
	defer stop()
	defer closeProcess()
	type res struct {
		b   []byte
		err error
	}
	type exit struct {
		code int
		err  error
	}
	outc, errc := make(chan res, 1), make(chan res, 1)
	waitc := make(chan exit, 1)
	go func() { b, err := io.ReadAll(p.Stdout); outc <- res{b, err} }()
	go func() {
		if p.Stderr == nil {
			errc <- res{}
			return
		}
		b, err := io.ReadAll(p.Stderr)
		errc <- res{b, err}
	}()
	go func() { code, err := p.Wait(); waitc <- exit{code, err} }()
	var out, stderrResult res
	var result exit
	for remaining := 3; remaining > 0; remaining-- {
		select {
		case out = <-outc:
			outc = nil
		case stderrResult = <-errc:
			errc = nil
		case result = <-waitc:
			waitc = nil
		case <-ctx.Done():
			closeProcess()
			return out.b, stderrResult.b, -1, ctx.Err()
		}
	}
	if ctx.Err() != nil {
		return out.b, stderrResult.b, -1, ctx.Err()
	}
	return out.b, stderrResult.b, result.code, errors.Join(result.err, out.err, stderrResult.err)
}
