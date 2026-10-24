package server

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/recipes"
	"strings"
	"testing"
)

type flatpakDiscoveryEngine struct {
	engine.Engine
	calls int
}

func (e *flatpakDiscoveryEngine) Exec(ctx context.Context, _ string, spec engine.ExecSpec) (*engine.Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.calls++
	out := ""
	switch spec.Cmd[0] {
	case "flatpak":
		if spec.Cmd[1] == "list" {
			out = "org.example.App\textra\n"
		} else {
			out = "extra\thttps://example.org/repo\n"
		}
	case "cat":
		out = "KEY"
	}
	return &engine.Process{Stdout: strings.NewReader(out), Stderr: strings.NewReader(""), Wait: func() (int, error) { return 0, nil }, Close: func() {}}, nil
}
func TestRunningFlatpakKeysOutliveDiscoveryContext(t *testing.T) {
	e := &flatpakDiscoveryEngine{}
	s := &Server{Eng: e}
	ctx, cancel := context.WithCancel(context.Background())
	c := desktops.Desktop{State: "running", Container: engine.Container{ID: "c1", State: "running"}}
	fl := s.desktopFlatpaks(ctx, c)
	calls := e.calls
	cancel()
	apps, remotes, notes := recipes.FlatpakParts(fl.apps, fl.remotes, fl.key)
	if len(apps) != 1 || len(remotes) != 1 || len(notes) != 0 {
		t.Fatalf("discovery lost key: apps=%v remotes=%v notes=%v", apps, remotes, notes)
	}
	if e.calls != calls {
		t.Fatal("key lookup performed deferred I/O")
	}
}
