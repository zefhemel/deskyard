package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// WithDataFolder wraps e for a supervisor whose data folder, data inside its
// container, is the engine host's folder host. Create maps each data folder
// source (a Mount without OnEngineHost) to host and refuses one outside
// data; engine-host sources pass unchanged. Inspect and List report sources
// in host as data folder paths again, without OnEngineHost. All other
// methods pass through.
//
// Without the wrapper, as in Go tests, an engine takes every source as an
// engine-host path: right when the data folder is at the same path on both.
func WithDataFolder(e Engine, data, host string) Engine {
	return dataFolder{Engine: e, data: filepath.Clean(data), host: filepath.Clean(host)}
}

type dataFolder struct {
	Engine
	data, host string
}

func (d dataFolder) Create(ctx context.Context, spec ContainerSpec) (string, error) {
	mounts := make([]Mount, len(spec.Mounts))
	for i, m := range spec.Mounts {
		if !m.OnEngineHost {
			rel, ok := below(m.Source, d.data)
			if !ok {
				return "", fmt.Errorf("mount source %s is not in the data folder %s", m.Source, d.data)
			}
			m.Source, m.OnEngineHost = filepath.Join(d.host, rel), true
		}
		mounts[i] = m
	}
	spec.Mounts = mounts
	return d.Engine.Create(ctx, spec)
}

func (d dataFolder) Inspect(ctx context.Context, id string) (Container, error) {
	c, err := d.Engine.Inspect(ctx, id)
	c.Mounts = d.fromHost(c.Mounts)
	return c, err
}

func (d dataFolder) List(ctx context.Context, labels map[string]string) ([]Container, error) {
	list, err := d.Engine.List(ctx, labels)
	if err != nil {
		return list, err
	}
	out := make([]Container, len(list))
	for i, c := range list {
		c.Mounts = d.fromHost(c.Mounts)
		out[i] = c
	}
	return out, nil
}

// fromHost returns a copy of mounts with the bind sources in the host
// folder as data folder paths, so the inner engine's slice is never
// modified.
func (d dataFolder) fromHost(mounts []Mount) []Mount {
	if mounts == nil {
		return nil
	}
	out := make([]Mount, len(mounts))
	for i, m := range mounts {
		if m.OnEngineHost && m.Volume == "" {
			if rel, ok := below(m.Source, d.host); ok {
				m.Source, m.OnEngineHost = filepath.Join(d.data, rel), false
			}
		}
		out[i] = m
	}
	return out
}

// below returns p relative to dir ("." for dir itself), and whether p is
// dir or inside it.
func below(p, dir string) (string, bool) {
	rel, err := filepath.Rel(dir, filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !filepath.IsAbs(p) {
		return "", false
	}
	return rel, true
}
