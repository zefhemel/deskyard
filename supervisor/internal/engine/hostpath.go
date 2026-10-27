package engine

import "context"

// WithHostPaths wraps e so mount sources are translated at the engine
// boundary: Create maps every source from the supervisor's own path to the
// host's through toHost, and Inspect and List map the sources they return
// back through fromHost. All other methods pass through.
func WithHostPaths(e Engine, toHost, fromHost func(string) string) Engine {
	return hostPaths{Engine: e, toHost: toHost, fromHost: fromHost}
}

type hostPaths struct {
	Engine
	toHost, fromHost func(string) string
}

func (h hostPaths) Create(ctx context.Context, spec ContainerSpec) (string, error) {
	spec.Mounts = mapMounts(spec.Mounts, h.toHost)
	return h.Engine.Create(ctx, spec)
}

func (h hostPaths) Inspect(ctx context.Context, id string) (Container, error) {
	c, err := h.Engine.Inspect(ctx, id)
	c.Mounts = mapMounts(c.Mounts, h.fromHost)
	return c, err
}

func (h hostPaths) List(ctx context.Context, labels map[string]string) ([]Container, error) {
	list, err := h.Engine.List(ctx, labels)
	if err != nil {
		return list, err
	}
	out := make([]Container, len(list))
	for i, c := range list {
		c.Mounts = mapMounts(c.Mounts, h.fromHost)
		out[i] = c
	}
	return out, nil
}

// mapMounts returns a copy of mounts with every source passed through f, so
// the caller's (or the inner engine's) slice is never modified.
func mapMounts(mounts []Mount, f func(string) string) []Mount {
	if mounts == nil {
		return nil
	}
	out := make([]Mount, len(mounts))
	for i, m := range mounts {
		m.Source = f(m.Source)
		out[i] = m
	}
	return out
}
