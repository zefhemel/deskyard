package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type progressCatalogEngine struct{ *catalogEngine }

func (e progressCatalogEngine) Pull(ctx context.Context, ref string, progress func(engine.PullProgress)) error {
	if progress == nil {
		panic("pull progress discarded")
	}
	progress(engine.PullProgress{Status: "Waiting"})
	progress(engine.PullProgress{Status: "Downloading", Current: 32 << 20, Total: 128 << 20})
	progress(engine.PullProgress{Status: "Extracting", Current: 96 << 20, Total: 128 << 20})
	return e.catalogEngine.Pull(ctx, ref, progress)
}

func TestCheckImagesReportsPullProgress(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			bt := newCatalogTest(t)
			bt.e.addImage("second", "sha256:second", "user")
			for _, entry := range []catalogEntry{{Ref: "second"}, {Ref: "missing"}, {Ref: "local", Custom: true}} {
				if err := bt.s.images.put(entry); err != nil {
					t.Fatal(err)
				}
			}
			bt.s.Eng = progressCatalogEngine{bt.e}
			if fail {
				bt.e.pullErr = errors.New("registry unavailable")
			}
			type update struct {
				message  string
				fraction float64
			}
			var updates []update
			err := bt.s.checkImages(context.Background(), func(message string, fraction float64) { updates = append(updates, update{message, fraction}) })
			if (err != nil) != fail {
				t.Fatalf("error: %v", err)
			}
			for _, want := range []update{
				{"Image 1 of 2 · img · Waiting", -1},
				{"Image 1 of 2 · img · Downloading · 32.0 of 128.0 MB", .25},
				{"Image 2 of 2 · second · Extracting · 96.0 of 128.0 MB", .75},
				{"Cleaning up unused old image versions", -1},
				{"Refreshing image catalog", -1},
			} {
				found := false
				for _, got := range updates {
					if got == want {
						found = true
					}
				}
				if !found {
					t.Errorf("missing update %+v in %+v", want, updates)
				}
			}
			for _, got := range updates {
				if strings.Contains(got.message, "missing") || strings.Contains(got.message, "local") {
					t.Fatalf("ineligible image in progress: %s", got.message)
				}
			}
		})
	}
}
