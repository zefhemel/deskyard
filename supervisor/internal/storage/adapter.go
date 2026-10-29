package storage

import (
	"context"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
)

// Computers is what backups need of the computers service: the
// definitions and volumes they back up, and the lifecycle a restore drives.
type Computers interface {
	Layout() config.Layout
	Definition(name string) (computers.Definition, bool)
	Volume(name string) (computers.Volume, bool)
	VolumeFolder(v computers.Volume) string
	VolumeUsers(name string) []computers.VolumeUser

	Get(ctx context.Context, name string) (computers.Computer, error)
	Reserve(name, state string) (func(), error)
	Stop(ctx context.Context, name string) error
	Start(ctx context.Context, name string) error
	Create(ctx context.Context, name string, set computers.Settings, progress func(string, float64)) (computers.Computer, error)
	CreateVolume(ctx context.Context, name, hostPath string) (computers.Volume, bool, error)
}

// ComputersOf adapts the computers service.
func ComputersOf(s *computers.Service) Computers { return envAdapter{s} }

type envAdapter struct{ *computers.Service }

func (a envAdapter) Layout() config.Layout { return a.Service.Layout }

func (a envAdapter) Definition(name string) (computers.Definition, bool) {
	return a.Service.Defs.Get(name)
}

func (a envAdapter) Volume(name string) (computers.Volume, bool) { return a.Service.Vols.Get(name) }
