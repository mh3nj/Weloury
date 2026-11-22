package containers

import (
	"context"
	"testing"
	"time"
)

// TestProviderRealDockerIntegration exercises Provider against an
// actual running Docker daemon - real /var/run/docker.sock, real
// `docker` binary, no fakes. It skips gracefully (not a failure) if
// Docker isn't available, so this test suite never breaks a machine
// without Docker installed; on a machine (or CI step) with Docker
// running, it's the strongest possible verification that the exact
// column layouts parse.go's fixtures assume are still what a real
// daemon actually produces, not just what a fixture snapshot says it
// once produced. See DEVELOPMENT.md for how this was run for real in
// this project's own sandbox (a temporary, manually-started dockerd -
// see there for exactly why it isn't a persistent part of the toolchain).
func TestProviderRealDockerIntegration(t *testing.T) {
	p := New()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	avail := p.Availability(ctx)
	if !avail.Available {
		t.Skipf("Docker not available in this environment (%s) - skipping real integration test", avail.Reason)
	}

	containers, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	t.Logf("found %d container(s) on the real daemon", len(containers))

	for _, c := range containers {
		if c.ID == "" {
			t.Error("expected every real container to have a non-empty ID")
		}
		if c.State == "running" && c.CPUPercent == nil {
			t.Errorf("container %s is running but has no CPUPercent from `docker stats` - either the real output format changed or the ID join failed", c.ID)
		}
	}

	if len(containers) > 0 {
		lines, err := p.Logs(ctx, containers[0].ID, 5)
		if err != nil {
			t.Errorf("Logs(%s): %v", containers[0].ID, err)
		}
		t.Logf("first container's last %d log line(s): %v", len(lines), lines)
	}
}
