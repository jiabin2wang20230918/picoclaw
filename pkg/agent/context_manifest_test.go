package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextBuilder_LoadBootstrapFiles_UsesManifestOrderAndFlags(t *testing.T) {
	workspace, err := os.MkdirTemp("", "picoclaw-context-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(workspace)

	files := map[string]string{
		"AGENT.md":    "agent content",
		"SOUL.md":     "soul content",
		"IDENTITY.md": "identity content",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	manifest := `{
	  "bootstrap_files": [
	    { "path": "IDENTITY.md" },
	    { "path": "AGENT.md" },
	    { "path": "SOUL.md", "enabled": false }
	  ]
	}`
	if err := os.WriteFile(filepath.Join(workspace, "context.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write context.json: %v", err)
	}

	builder := NewContextBuilder(workspace)
	content := builder.LoadBootstrapFiles()

	if !strings.Contains(content, "identity content") || !strings.Contains(content, "agent content") {
		t.Fatalf("expected manifest-selected files in output, got: %s", content)
	}
	if strings.Contains(content, "soul content") {
		t.Fatalf("expected disabled bootstrap file to be excluded, got: %s", content)
	}

	identityIdx := strings.Index(content, "IDENTITY.md")
	agentIdx := strings.Index(content, "AGENT.md")
	if identityIdx == -1 || agentIdx == -1 || identityIdx > agentIdx {
		t.Fatalf("expected manifest order to be respected, got: %s", content)
	}
}
