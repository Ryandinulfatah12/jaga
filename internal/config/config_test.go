// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func noSearch(t *testing.T) {
	t.Helper()
	old := searchPaths
	searchPaths = []string{filepath.Join(t.TempDir(), "absent.yml")}
	t.Cleanup(func() { searchPaths = old })
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "jaga.yml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsWithoutFile(t *testing.T) {
	noSearch(t)
	cfg, path, err := Load("", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || cfg != Default() {
		t.Errorf("got %+v from %q, want defaults", cfg, path)
	}
}

func TestPrecedenceFileThenEnv(t *testing.T) {
	noSearch(t)
	p := writeFile(t, "listen: \":9000\"\nstorage:\n  disk_quota_percent: 80\n")
	cfg, path, err := Load("", env(map[string]string{
		"JAGA_CONFIG":                     p,
		"JAGA_STORAGE_DISK_QUOTA_PERCENT": "85",
		"JAGA_GO2RTC_API":                 "[::1]:2000",
		"JAGA_TRUST_PROXY":                "true",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if path != p {
		t.Errorf("path = %q, want %q", path, p)
	}
	if cfg.Listen != ":9000" || cfg.Storage.DiskQuotaPercent != 85 || cfg.Go2RTC.API != "[::1]:2000" || !cfg.TrustProxy {
		t.Errorf("got %+v", cfg)
	}
	if cfg.Storage.DefaultRetentionDays != 7 {
		t.Errorf("unset key lost its default: %+v", cfg.Storage)
	}
}

func TestInvalid(t *testing.T) {
	noSearch(t)
	cases := map[string]struct {
		file string
		env  map[string]string
	}{
		"unknown key":        {file: "lisen: \":80\"\n"},
		"bad yaml":           {file: "listen: [\n"},
		"env not a number":   {env: map[string]string{"JAGA_STORAGE_DISK_QUOTA_PERCENT": "lots"}},
		"env not a bool":     {env: map[string]string{"JAGA_TRUST_PROXY": "yes please"}},
		"quota out of range": {env: map[string]string{"JAGA_STORAGE_DISK_QUOTA_PERCENT": "100"}},
		"bad go2rtc mode":    {env: map[string]string{"JAGA_GO2RTC_MODE": "docker"}},
		"go2rtc api public":  {env: map[string]string{"JAGA_GO2RTC_API": "0.0.0.0:1984"}},
		"go2rtc api no port": {env: map[string]string{"JAGA_GO2RTC_API": "127.0.0.1"}},
		"go2rtc api lan":     {env: map[string]string{"JAGA_GO2RTC_API": "192.168.1.5:1984"}},
		"webrtc port":        {env: map[string]string{"JAGA_GO2RTC_WEBRTC_PORT": "0"}},
		"unexpanded secret":  {file: "secret_key: ${JAGA_SECRET_KEY}\n"},
		"missing --config":   {env: map[string]string{"JAGA_CONFIG": "/nonexistent/jaga.yml"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := map[string]string{}
			for k, v := range tc.env {
				e[k] = v
			}
			if tc.file != "" {
				e["JAGA_CONFIG"] = writeFile(t, tc.file)
			}
			if _, _, err := Load("", env(e)); !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestExplicitBeatsEnvConfig(t *testing.T) {
	noSearch(t)
	p := writeFile(t, "listen: \":7000\"\n")
	cfg, _, err := Load(p, env(map[string]string{"JAGA_CONFIG": "/nonexistent/jaga.yml"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":7000" {
		t.Errorf("listen = %q", cfg.Listen)
	}
}
