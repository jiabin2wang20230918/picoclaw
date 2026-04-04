package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type BootstrapFileSpec struct {
	Path     string `json:"path"`
	Required bool   `json:"required,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

type WorkspaceContextManifest struct {
	BootstrapFiles []BootstrapFileSpec `json:"bootstrap_files"`
}

var defaultBootstrapFiles = []BootstrapFileSpec{
	{Path: "AGENT.md"},
	{Path: "SOUL.md"},
	{Path: "USER.md"},
	{Path: "IDENTITY.md"},
}

func loadWorkspaceContextManifest(workspace string) WorkspaceContextManifest {
	manifestPath := filepath.Join(workspace, "context.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return WorkspaceContextManifest{BootstrapFiles: defaultBootstrapFiles}
	}

	var manifest WorkspaceContextManifest
	if err := json.Unmarshal(data, &manifest); err != nil || len(manifest.BootstrapFiles) == 0 {
		return WorkspaceContextManifest{BootstrapFiles: defaultBootstrapFiles}
	}

	return manifest
}

func resolveBootstrapFiles(workspace string) []BootstrapFileSpec {
	manifest := loadWorkspaceContextManifest(workspace)
	if len(manifest.BootstrapFiles) == 0 {
		return defaultBootstrapFiles
	}
	return manifest.BootstrapFiles
}
