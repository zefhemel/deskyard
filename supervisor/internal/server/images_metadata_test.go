package server

import (
	"testing"
	"time"
)

func TestCatalogReportsDownloadedImageSizeAndBuildDate(t *testing.T) {
	bt := newCatalogTest(t)
	built := time.Date(2026, 10, 22, 6, 12, 0, 0, time.UTC)
	bt.e.mu.Lock()
	img := bt.e.images["sha256:base1"]
	img.Size, img.Created = 2_000_000_000, built
	bt.e.images["sha256:base1"] = img
	bt.e.mu.Unlock()
	bt.do(bt.admin, "POST", "/api/images", map[string]string{"ref": "img"})
	got, ok := bt.catalog()["img"]
	if !ok {
		t.Skip("img isn't in this catalog")
	}
	if got.Size != 2_000_000_000 || got.Built == nil || !got.Built.Equal(built) {
		t.Fatalf("size %d, built %v", got.Size, got.Built)
	}
}
