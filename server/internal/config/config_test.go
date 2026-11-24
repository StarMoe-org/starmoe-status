package config_test

import (
	"path/filepath"
	"testing"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
)

func TestLoadMonitorsConfigFiles(t *testing.T) {
	for _, rel := range []string{"../../../config/monitors.example.yaml", "../../../config/monitors.yaml"} {
		path, _ := filepath.Abs(rel)
		mf, err := config.LoadMonitorsFile(path)
		if err != nil {
			t.Fatalf("LoadMonitorsFile(%s): %v", path, err)
		}
		if len(mf.Groups) != 3 {
			t.Fatalf("expected 3 public groups in %s, got %d", path, len(mf.Groups))
		}
		byID := make(map[string]config.MonitorConfig, len(mf.Monitors))
		for _, m := range mf.Monitors {
			byID[m.ID] = m
		}
		for _, internalID := range []string{"internal-sll", "internal-bili"} {
			m, ok := byID[internalID]
			if !ok {
				t.Fatalf("missing %s in %s", internalID, path)
			}
			if m.Visibility != protocol.VisibilityInternal {
				t.Fatalf("%s must have visibility=internal, got %q", internalID, m.Visibility)
			}
		}
		for _, pubID := range []string{"starmoe-passport", "pjsk-web", "bdon-web", "infra-de", "infra-jp1", "infra-jp2", "infra-us"} {
			m, ok := byID[pubID]
			if !ok {
				t.Fatalf("missing %s in %s", pubID, path)
			}
			if m.Visibility != protocol.VisibilityPublic {
				t.Fatalf("%s must have visibility=public, got %q", pubID, m.Visibility)
			}
		}
	}
}
