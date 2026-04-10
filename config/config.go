package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectDirs are valid parent directories for beads mounts.
// Mounts must be exactly 1 level deep from one of these.
// Set via BEADS_PROJECT_DIRS (colon-separated), defaults to ~/src:~/prj.
var ProjectDirs []string

func init() {
	home := os.Getenv("HOME")
	s := os.Getenv("BEADS_PROJECT_DIRS")
	if s == "" {
		s = home + "/src:" + home + "/prj"
	}
	for _, dir := range strings.Split(s, ":") {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(home, dir[2:])
		}
		ProjectDirs = append(ProjectDirs, dir)
	}
}
