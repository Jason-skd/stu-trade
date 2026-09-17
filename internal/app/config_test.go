package app

import (
	"log/slog"
	"testing"
)

func TestLoadConfigProfiles(t *testing.T) {
	tests := []struct {
		name          string
		profile       string
		databaseURL   string
		wantAddr      string
		wantMigration bool
	}{
		{name: "native defaults", profile: "native", wantAddr: "127.0.0.1:8080", wantMigration: true},
		{name: "develop", profile: "develop", databaseURL: "postgres://user:pass@localhost:5432/app", wantAddr: ":8080", wantMigration: true},
		{name: "production", profile: "production", databaseURL: "postgres://user:pass@database:5432/app", wantAddr: ":8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnvironment(t)
			if tt.databaseURL != "" {
				t.Setenv("STUTRADE_DATABASE_URL", tt.databaseURL)
			}
			cfg, err := LoadConfig(LoadOptions{Profile: tt.profile, Version: "v1.2.3", Commit: "abc123"})
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if string(cfg.Profile) != tt.profile || cfg.HTTPAddr != tt.wantAddr || cfg.RunMigrations != tt.wantMigration {
				t.Fatalf("profile config = (%q, %q, %t)", cfg.Profile, cfg.HTTPAddr, cfg.RunMigrations)
			}
			if cfg.Version != "v1.2.3" || cfg.Commit != "abc123" || cfg.LogLevel != slog.LevelInfo {
				t.Fatalf("build config = (%q, %q, %v)", cfg.Version, cfg.Commit, cfg.LogLevel)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		env     map[string]string
	}{
		{name: "unknown profile", profile: "staging"},
		{name: "production database missing", profile: "production"},
		{name: "database is not PostgreSQL", profile: "develop", env: map[string]string{"STUTRADE_DATABASE_URL": "mysql://localhost/app"}},
		{name: "database name missing", profile: "develop", env: map[string]string{"STUTRADE_DATABASE_URL": "postgres://localhost"}},
		{name: "invalid HTTP address", profile: "native", env: map[string]string{"STUTRADE_HTTP_ADDR": "8080"}},
		{name: "invalid HTTP port", profile: "native", env: map[string]string{"STUTRADE_HTTP_ADDR": ":70000"}},
		{name: "invalid log level", profile: "native", env: map[string]string{"STUTRADE_LOG_LEVEL": "verbose"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnvironment(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			if _, err := LoadConfig(LoadOptions{Profile: tt.profile}); err == nil {
				t.Fatal("LoadConfig() error = nil")
			}
		})
	}
}

func TestLoadConfigUsesExplicitProfileBeforeEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("STUTRADE_PROFILE", "production")
	cfg, err := LoadConfig(LoadOptions{Profile: "native"})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Profile != ProfileNative {
		t.Fatalf("Profile = %q, want %q", cfg.Profile, ProfileNative)
	}
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"STUTRADE_PROFILE",
		"STUTRADE_HTTP_ADDR",
		"STUTRADE_DATABASE_URL",
		"STUTRADE_LOG_LEVEL",
	} {
		t.Setenv(key, "")
	}
}
