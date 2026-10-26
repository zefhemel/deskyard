package engine

import "context"

// ImagePruner is optional: engines that can list and delete images let the
// supervisor delete desktop image versions that nothing uses any more.
type ImagePruner interface {
	// DanglingImages lists images that no tag names any more (such as an old
	// version a newer pull of its tag replaced) and that carry all of labels.
	DanglingImages(ctx context.Context, labels map[string]string) ([]Image, error)
	// RemoveImage deletes an image by ID without forcing, so the engine
	// refuses while any container uses it.
	RemoveImage(ctx context.Context, id string) error
}
