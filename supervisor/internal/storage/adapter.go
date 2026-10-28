package storage

import (
	"context"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// Environments is what backups need of the environments service: the
// definitions and volumes they back up, and the lifecycle a restore drives.
type Environments interface {
	Layout() config.Layout
	Definition(name string) (environments.Definition, bool)
	Volume(name string) (environments.Volume, bool)
	VolumeFolder(v environments.Volume) environments.Folder
	VolumeUsers(name string) []environments.VolumeUser
	// RemoveTree and MakeFreeFolder change folders, as root through a
	// helper where the supervisor can't.
	RemoveTree(ctx context.Context, parent environments.Folder, child string) error
	MakeFreeFolder(ctx context.Context, parent environments.Folder, base string) (string, error)

	// HoldFiles and FilesActivity are the reservations of items' files.
	HoldFiles(it environments.Item, what string, replacing bool) (func(), error)
	FilesActivity(it environments.Item) string

	Get(ctx context.Context, name string) (environments.Environment, error)
	Reserve(name, state string) (func(), error)
	Stop(ctx context.Context, name string) error
	Start(ctx context.Context, name string) error
	ClaimName(name string) (func(), error)
	CreateStopped(ctx context.Context, name string, set environments.Settings, progress func(string, float64)) (environments.Environment, error)
	CreateVolume(ctx context.Context, name, hostPath string) (environments.Volume, bool, error)
}

// EnvironmentsOf adapts the environments service.
func EnvironmentsOf(s *environments.Service) Environments { return envAdapter{s} }

type envAdapter struct{ *environments.Service }

func (a envAdapter) Layout() config.Layout { return a.Service.Layout }

func (a envAdapter) Definition(name string) (environments.Definition, bool) {
	return a.Service.Defs.Get(name)
}

func (a envAdapter) Volume(name string) (environments.Volume, bool) { return a.Service.Vols.Get(name) }
