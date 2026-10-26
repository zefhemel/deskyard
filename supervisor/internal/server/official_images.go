package server

import "slices"

// Built-in choices are metadata, not downloads or persisted catalog entries.
// Their references match the desktop targets published by images.yml.
type officialImage struct {
	Ref, Title, Description string
}

var officialImages = []officialImage{
	{"ghcr.io/zefhemel/deskyard-debian-xfce:latest", "Debian XFCE", "A lightweight desktop with everyday apps, Chromium and Software for installing more."},
}

func officialImageFor(ref string) (officialImage, bool) {
	for _, image := range officialImages {
		if sameRef(image.Ref, ref) {
			return image, true
		}
	}
	return officialImage{}, false
}

func availableEntries(saved []catalogEntry) []catalogEntry {
	entries := slices.Clone(saved)
	for _, image := range officialImages {
		if !slices.ContainsFunc(entries, func(e catalogEntry) bool { return sameRef(e.Ref, image.Ref) }) {
			entries = append(entries, catalogEntry{Ref: image.Ref})
		}
	}
	return entries
}
