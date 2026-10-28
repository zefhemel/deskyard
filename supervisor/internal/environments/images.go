package environments

import (
	"context"
	"errors"
	"fmt"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func (s *Service) image(ctx context.Context, ref string) engine.Image {
	s.mu.Lock()
	img, ok := s.imgInfo[ref]
	s.mu.Unlock()
	if ok {
		return img
	}
	img, err := s.Eng.ImageInspect(ctx, ref)
	if err != nil {
		return engine.Image{}
	}
	s.mu.Lock()
	s.imgInfo[ref] = img
	s.mu.Unlock()
	return img
}

// ForgetImage drops cached image information (after a pull).
func (s *Service) ForgetImage(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.imgInfo, ref)
}

// ImagesInUse returns the IDs of the images that environment definitions pin
// (stopped environments without a container included) and that containers
// on the engine run from, of any supervisor and in any state, parked
// replacements included.
func (s *Service) ImagesInUse(ctx context.Context) (map[string]bool, error) {
	inUse := map[string]bool{}
	for _, def := range s.Defs.List() {
		if def.Image.ID != "" {
			inUse[def.Image.ID] = true
		}
	}
	all, err := s.Eng.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		inUse[c.ImageID] = true
	}
	return inUse, nil
}

// EnsureImage pulls ref when it isn't present and checks the image contract.
func (s *Service) EnsureImage(ctx context.Context, ref string, progress func(string, float64)) (engine.Image, error) {
	img, err := s.Eng.ImageInspect(ctx, ref)
	if errors.Is(err, engine.ErrNotFound) {
		progress("Pulling "+ref, 0)
		err = s.Eng.Pull(ctx, ref, func(p engine.PullProgress) {
			if p.Total > 0 {
				progress(fmt.Sprintf("Pulling image, %d of %d MB", p.Current>>20, p.Total>>20), float64(p.Current)/float64(p.Total))
			}
		})
		if err != nil {
			return engine.Image{}, fmt.Errorf("pulling %s: %w", ref, err)
		}
		s.ForgetImage(ref)
		img, err = s.Eng.ImageInspect(ctx, ref)
	}
	if err != nil {
		return engine.Image{}, err
	}
	if img.Labels[LabelContract] != Contract {
		return engine.Image{}, fmt.Errorf("%s isn't a Deskyard desktop image (label %s=%s missing)", ref, LabelContract, Contract)
	}
	if s.ImageUsed != nil {
		s.ImageUsed(ref, img.ID)
	}
	return img, nil
}

// pinned is the image a definition's container is built from.
func pinned(def Definition) string {
	if def.Image.ID != "" {
		return def.Image.ID
	}
	return def.Image.Ref
}
