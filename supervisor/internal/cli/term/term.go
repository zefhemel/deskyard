// Package term puts the local terminal into raw mode and reads its size,
// for `computer shell`, without dependencies beyond golang.org/x/sys.
package term

// State is a saved terminal state to restore.
type State struct{ state }
