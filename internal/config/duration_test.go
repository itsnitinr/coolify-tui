package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDurationUnmarshal(t *testing.T) {
	tests := []struct {
		yaml    string
		want    time.Duration
		wantErr bool
	}{
		{yaml: `refresh_interval: 10s`, want: 10 * time.Second},
		{yaml: `refresh_interval: 1m30s`, want: 90 * time.Second},
		{yaml: `refresh_interval: "45s"`, want: 45 * time.Second},
		{yaml: `refresh_interval: 30`, want: 30 * time.Second},
		{yaml: `refresh_interval: banana`, wantErr: true},
		{yaml: `refresh_interval: [1,2]`, wantErr: true},
	}
	for _, tc := range tests {
		dir := t.TempDir()
		t.Setenv("COOLIFY_TUI_CONFIG_DIR", dir)
		path := filepath.Join(dir, FileName)
		body := tc.yaml + "\ninstances:\n  - name: a\n    url: u\n    token: 1|s\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cfg, err := Load()
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: want parse error, got nil", tc.yaml)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: Load: %v", tc.yaml, err)
			continue
		}
		if got := cfg.RefreshInterval.Std(); got != tc.want {
			t.Errorf("%q: RefreshInterval = %v, want %v", tc.yaml, got, tc.want)
		}
	}
}

func TestDurationString(t *testing.T) {
	if got := Duration(90 * time.Second).String(); got != "1m30s" {
		t.Errorf("String() = %q, want 1m30s", got)
	}
}
