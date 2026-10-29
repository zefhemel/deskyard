// Package engine is the supervisor's own view of a container engine. All
// supervisor logic goes through this interface; only engine/docker knows the
// Docker SDK, so a Podman implementation can be added beside it.
package engine

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned for a container, network or image that doesn't exist.
var ErrNotFound = errors.New("not found")

// ErrSubnetTaken is returned by CreateNetwork when another network already
// uses part of the subnet, possibly one created since it was chosen.
var ErrSubnetTaken = errors.New("subnet in use")

type Mount struct {
	Source, Target string // Source is a host path
	// Volume is the Docker volume's name when an inspected mount is a named
	// volume, empty for a bind mount. Create ignores it.
	Volume   string
	ReadOnly bool
	// MustExist uses a strict bind: a missing host source is an error, never created.
	MustExist bool
}

type ContainerSpec struct {
	Name, Image string
	Labels      map[string]string
	Env         map[string]string
	Mounts      []Mount
	// NetworkContainer shares this existing container's network namespace.
	// Mutually exclusive with Network and Aliases.
	NetworkContainer string
	// CapAdd explicitly grants capabilities; desktop defaults remain unchanged.
	CapAdd      []string
	Network     string
	Aliases     []string
	MemoryBytes int64
	NanoCPUs    int64
	ShmBytes    int64
	// Flatpak runs the container with the security options Flatpak's sandbox
	// needs (seccomp profile, apparmor and system paths unconfined).
	Flatpak bool
	Restart string // "unless-stopped", "no"
	Cmd     []string
	// Devices are host devices passed in at the same path (e.g. /dev/net/tun).
	Devices []string
	// Entrypoint replaces the image's entrypoint when set.
	Entrypoint []string
	// NoHealthcheck turns off the image's HEALTHCHECK, which would otherwise
	// run the image's own command in the container.
	NoHealthcheck bool
	// NoNewPrivileges prevents setuid and file capabilities granting new privileges.
	NoNewPrivileges bool
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
	MemoryBytes, NanoCPUs int64
	Devices               []string // host devices passed in (their paths on the host)
	Mounts                []Mount  // its mounts, Source on the host
}

func (c Container) Running() bool { return c.State == "running" || c.State == "restarting" }

type ExecSpec struct {
	// DesktopSession joins the image's shared user/mount namespace when supported.
	// Older images retain ordinary exec behavior. Callers must use the desktop user.
	DesktopSession bool
	Cmd            []string
	User           string
	WorkDir        string
	Env            []string
	Tty            bool
	Stdin          bool
	Cols, Rows     uint
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
	// Close disposes attached I/O, unblocking reads and writes. It is safe to
	// call repeatedly or concurrently; command termination is not guaranteed.
	// Stdin.Close is separate: non-TTY execs retain output after end of input.
	Close func()
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
	// Size is its size on disk in bytes; Created when it was built (zero
	// when the engine doesn't say).
	Size    int64
	Created time.Time
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
	// Rename gives a container another name; its ID stays.
	Rename(ctx context.Context, id, name string) error
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

	Events(ctx context.Context) (<-chan Event, error)
	Info(ctx context.Context) (HostInfo, error)
}

// Output runs cmd with bounded capture and returns its stdout, stderr and exit code.
func Output(ctx context.Context, e Engine, id string, spec ExecSpec) (stdout, stderr []byte, code int, err error) {
	result, err := Run(ctx, e, id, spec, RunOptions{})
	return result.Stdout, result.Stderr, result.Code, err
}
