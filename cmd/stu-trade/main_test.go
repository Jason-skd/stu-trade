package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// === 验证命令行为 ===

func TestRunVersion(t *testing.T) {
	originalVersion, originalCommit := version, commit
	version, commit = "v1.2.3", "abc123"
	t.Cleanup(func() {
		version, commit = originalVersion, originalCommit
	})

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(version) error = %v", err)
	}
	if got := stdout.String(); got != "v1.2.3 (abc123)\n" {
		t.Fatalf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRejectsUnknownCommandBeforeLoadingConfig(t *testing.T) {
	t.Setenv("STUTRADE_PROFILE", "invalid")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"unknown"}, &stdout, &stderr)
	if err == nil || !strings.HasPrefix(err.Error(), "usage:") {
		t.Fatalf("run(unknown) error = %v, want usage", err)
	}
}

func TestRunRequiresMigrationDirection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"migrate"}, &stdout, &stderr)
	if err == nil || !strings.HasPrefix(err.Error(), "usage:") {
		t.Fatalf("run(migrate) error = %v, want usage", err)
	}
}
