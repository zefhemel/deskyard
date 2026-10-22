//go:build !linux

package imagescan

// capability: file capabilities exist on Linux only.
func capability(string) ([]byte, error) { return nil, nil }
