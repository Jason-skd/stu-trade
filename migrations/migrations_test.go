package migrations

import "testing"

func TestLatestVersionFollowsEmbeddedMigrations(t *testing.T) {
	if got := LatestVersion(); got != 1 {
		t.Fatalf("LatestVersion() = %d, want 1", got)
	}
}
