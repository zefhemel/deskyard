package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// The volume commands.

func runVolumeList(a *App, c *call, cl *remote.Client) error {
	var list struct {
		Volumes []struct {
			Name         string  `json:"name"`
			Folder       string  `json:"folder"`
			Created      *string `json:"created"`
			Environments []mount `json:"environments"`
		} `json:"volumes"`
	}
	if done, err := getJSON(a, c, cl, "/volumes", &list); done || err != nil {
		return err
	}
	if len(list.Volumes) == 0 {
		fmt.Fprintln(a.Stdout, "No volumes: create one with `deskyard volume create NAME`.")
		return nil
	}
	var rows [][]string
	for _, v := range list.Volumes {
		rows = append(rows, []string{v.Name, formatMounts(v.Environments), v.Folder, when(v.Created)})
	}
	return table(a.Stdout, []string{"NAME", "ENVIRONMENTS", "FOLDER", "CREATED"}, rows)
}

func runVolumeCreate(a *App, c *call, cl *remote.Client) error {
	var ans struct {
		Volume struct {
			Name   string `json:"name"`
			Folder string `json:"folder"`
		} `json:"volume"`
		Adopted bool `json:"adopted"`
	}
	if err := cl.JSON(c.ctx, http.MethodPost, "/volumes", map[string]string{"name": c.args[0]}, &ans); err != nil {
		return err
	}
	if ans.Adopted {
		fmt.Fprintf(a.Stdout, "Created the volume %s, keeping the existing folder %s in the data folder.\n", ans.Volume.Name, ans.Volume.Folder)
	} else {
		fmt.Fprintf(a.Stdout, "Created the volume %s (%s in the data folder). Grant it with `deskyard env create … --volume %s`.\n", ans.Volume.Name, ans.Volume.Folder, ans.Volume.Name)
	}
	return nil
}

func runVolumeRemove(a *App, c *call, cl *remote.Client) error {
	name, del := c.args[0], c.bool("--delete-files")
	if err := cl.JSONWithin(c.ctx, removalTimeout, http.MethodDelete, "/volumes/"+url.PathEscape(name)+"?deleteFiles="+strconv.FormatBool(del), nil, nil); err != nil {
		return err
	}
	if del {
		fmt.Fprintf(a.Stdout, "Removed the volume %s and its files.\n", name)
	} else {
		fmt.Fprintf(a.Stdout, "Removed the volume %s; its folder is kept.\n", name)
	}
	return nil
}
