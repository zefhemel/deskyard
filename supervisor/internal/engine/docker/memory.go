package docker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// reclaimImage is the memory helper's: the pinned image of every other
// helper, whose busybox has the shell tools reclaimScript uses. The helper
// runner can't run it, as it needs the host's cgroup namespace and a
// hardened, read-only container the engine interface doesn't describe.
const reclaimImage = helper.Image
const reclaimBytes = 128 << 20

var _ engine.MemoryReclaimer = (*Docker)(nil)

func (d *Docker) ReclaimMemory(ctx context.Context, id string) (engine.MemoryReclaim, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	image, err := d.memoryHelperImage(ctx)
	if err != nil {
		return engine.MemoryReclaim{}, err
	}
	target, err := d.c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return engine.MemoryReclaim{}, wrap(err)
	}
	c := target.Container
	if c.State == nil || !c.State.Running || c.State.Paused || c.State.Pid <= 0 {
		return engine.MemoryReclaim{}, errors.New("the environment must be running to reclaim memory")
	}
	// Discover the path through one read-only proc file. No host PID namespace,
	// Docker socket, or writable host filesystem is exposed to this helper.
	out, err := d.memoryHelper(ctx, image, []mount.Mount{{Type: mount.TypeBind,
		Source: fmt.Sprintf("/proc/%d/cgroup", c.State.Pid), Target: "/target-cgroup", ReadOnly: true}},
		`sed -n 's/^0:://p' /target-cgroup`)
	if err != nil {
		return engine.MemoryReclaim{}, fmt.Errorf("discovering the environment cgroup: %w", err)
	}
	group, err := reclaimCgroup(string(out), c.ID)
	if err != nil {
		return engine.MemoryReclaim{}, err
	}
	// Bind only this desktop's cgroup, and use a strict bind mount (not -v):
	// if the target disappears, Docker must not recreate a host directory.
	out, err = d.memoryHelper(ctx, image, []mount.Mount{{Type: mount.TypeBind,
		Source: path.Join("/sys/fs/cgroup", group), Target: "/target"}}, reclaimScript)
	if err != nil {
		return engine.MemoryReclaim{}, err
	}
	var result engine.MemoryReclaim
	if err := json.Unmarshal(out, &result); err != nil {
		return result, fmt.Errorf("invalid memory helper result: %w", err)
	}
	result.RequestedBytes = reclaimBytes
	return result, nil
}

var cgroupPath = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

// Require the exact full container ID in a directory component; reject paths
// that could escape the cgroup filesystem or name an ancestor/another desktop.
func reclaimCgroup(output, id string) (string, error) {
	group := strings.TrimSpace(output)
	if len(id) != 64 || !cgroupPath.MatchString(group) || path.Clean(group) != group {
		return "", fmt.Errorf("%w: cannot identify this environment's cgroup", engine.ErrReclaimUnsupported)
	}
	for _, part := range strings.Split(group, "/") {
		if part == id || part == "docker-"+id+".scope" {
			return group, nil
		}
	}
	return "", fmt.Errorf("%w: cgroup does not belong to this environment", engine.ErrReclaimUnsupported)
}

// memoryHelper runs only our fixed shell program from the resolved helper image
// ID, with no capabilities/network, a read-only root and bounded resources/output.
func (d *Docker) createMemoryHelper(ctx context.Context, image string, mounts []mount.Mount, script string) (string, error) {
	pids := int64(16)
	created, err := d.c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: "deskyard-reclaim-" + strings.ToLower(rand.Text()[:12]),
		Config: &container.Config{Image: image, User: "0:0", Entrypoint: []string{"/bin/sh"},
			Cmd: []string{"-ec", script}, Env: []string{"LC_ALL=C"},
			Labels:      map[string]string{helper.LabelHelper: "memory-reclaim"},
			Healthcheck: &container.HealthConfig{Test: []string{"NONE"}}},
		HostConfig: &container.HostConfig{NetworkMode: "none", ReadonlyRootfs: true,
			CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"},
			CgroupnsMode: container.CgroupnsModeHost, Mounts: mounts,
			Resources: container.Resources{Memory: 32 << 20, NanoCPUs: 100_000_000, PidsLimit: &pids}},
	})
	if err != nil {
		return "", fmt.Errorf("creating memory helper: %w", err)
	}
	return created.ID, nil
}

func (d *Docker) removeMemoryHelper(ctx context.Context, id string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := d.c.ContainerRemove(cleanup, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if err != nil {
		return fmt.Errorf("removing memory helper: %w", err)
	}
	return nil
}

func (d *Docker) memoryHelper(ctx context.Context, image string, mounts []mount.Mount, script string) (_ []byte, retErr error) {
	id, err := d.createMemoryHelper(ctx, image, mounts, script)
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, d.removeMemoryHelper(ctx, id))
	}()
	if _, err = d.c.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return nil, err
	}
	wait := d.c.ContainerWait(ctx, id, client.ContainerWaitOptions{})
	var exit int64
	select {
	case status := <-wait.Result:
		exit = status.StatusCode
		if status.Error != nil {
			return nil, errors.New(status.Error.Message)
		}
	case err := <-wait.Error:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	logs, err := d.c.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return nil, err
	}
	defer logs.Close()
	var stdout, stderr bytes.Buffer
	if _, err = stdcopy.StdCopy(&stdout, &stderr, io.LimitReader(logs, 16<<10)); err != nil {
		return nil, err
	}
	if exit == 20 {
		return nil, fmt.Errorf("%w: %s", engine.ErrReclaimUnsupported, strings.TrimSpace(stderr.String()))
	}
	if exit != 0 {
		return nil, fmt.Errorf("memory helper exited %s: %s", strconv.FormatInt(exit, 10), strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// BusyBox's C-locale EAGAIN diagnostic means the kernel reclaimed less than
// requested. Preserve measurements and report partial success; other write
// errors remain failures. Never lower limits, drop global caches or freeze apps.
const reclaimScript = `
unsupported() { echo "$1" >&2; exit 20; }
test -w /target/memory.reclaim || unsupported 'memory.reclaim is unavailable or read-only'
test -r /target/memory.swap.current || unsupported 'swap accounting is unavailable'
test "$(cat /target/memory.swap.max)" != 0 || unsupported 'swap is disabled for this environment'
awk 'NR>1 && $3>0 {found=1} END {exit !found}' /proc/swaps || unsupported 'the Linux host has no swap'
grep -q '^populated 1$' /target/cgroup.events || { echo 'the environment has stopped' >&2; exit 1; }
before=$(cat /target/memory.current)
swap_before=$(cat /target/memory.swap.current)
partial=false
if error=$(printf '%s' '128M swappiness=100' | dd of=/target/memory.reclaim bs=64 count=1 2>&1); then
  :
else
  case "$error" in
    *'Resource temporarily unavailable'*) partial=true ;;
    *'Invalid argument'*) unsupported 'this kernel does not support reclaim with swappiness' ;;
    *) echo "$error" >&2; exit 1 ;;
  esac
fi
sleep 1
after=$(cat /target/memory.current)
swap_after=$(cat /target/memory.swap.current)
printf '{"beforeBytes":%s,"afterBytes":%s,"swapBeforeBytes":%s,"swapAfterBytes":%s,"partial":%s}\n' "$before" "$after" "$swap_before" "$swap_after" "$partial"
`
