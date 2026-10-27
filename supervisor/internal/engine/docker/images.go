package docker

import (
	"context"
	"time"

	"github.com/moby/moby/client"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

var _ engine.ImagePruner = (*Docker)(nil)

func (d *Docker) DanglingImages(ctx context.Context, labels map[string]string) ([]engine.Image, error) {
	f := labelFilters(labels)
	f.Add("dangling", "true")
	res, err := d.c.ImageList(ctx, client.ImageListOptions{Filters: f})
	if err != nil {
		return nil, wrap(err)
	}
	out := make([]engine.Image, 0, len(res.Items))
	for _, s := range res.Items {
		out = append(out, engine.Image{ID: s.ID, Labels: s.Labels, Size: s.Size, Created: time.Unix(s.Created, 0).UTC()})
	}
	return out, nil
}

func (d *Docker) RemoveImage(ctx context.Context, id string) error {
	// Untagged parents (locally built images have them) go too, as with
	// `docker rmi`.
	_, err := d.c.ImageRemove(ctx, id, client.ImageRemoveOptions{PruneChildren: true})
	return wrap(err)
}
