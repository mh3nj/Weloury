package containers

import "testing"

// Fixtures below are captured verbatim from a real, running Docker
// 29.1.3 daemon in this project's own sandbox (see DEVELOPMENT.md) -
// `docker ps -a --format '{{json .}}'` / `docker stats --no-stream
// --format '{{json .}}'` against real containers built from a minimal
// `FROM scratch` image (no registry access was available to pull a
// public image - see provider_test.go's doc comment) - not hand-written
// or guessed from documentation.

const fixturePSRunningWithPort = `{"Command":"\"/app\"","CreatedAt":"2026-08-25 11:18:33 +0000 UTC","ID":"69984edd6de7","Image":"weloury-test:latest","Labels":"","LocalVolumes":"0","Mounts":"","Names":"weloury-test-nginx","Networks":"bridge","Platform":{"architecture":"amd64","os":"linux"},"Ports":"0.0.0.0:18080->8080/tcp","RunningFor":"About a minute ago","Size":"4.1kB (virtual 7.04MB)","State":"running","Status":"Up 3 seconds"}`

const fixturePSExitedNoPort = `{"Command":"\"/app\"","CreatedAt":"2026-08-25 11:18:42 +0000 UTC","ID":"0e0e974a281f","Image":"weloury-test:latest","Labels":"","LocalVolumes":"0","Mounts":"","Names":"weloury-test-noport","Networks":"bridge","Platform":{"architecture":"amd64","os":"linux"},"Ports":"","RunningFor":"56 seconds ago","Size":"4.1kB (virtual 7.04MB)","State":"exited","Status":"Exited (2) Less than a second ago"}`

const fixtureStatsRunning = `{"BlockIO":"0B / 0B","CPUPerc":"0.00%","Container":"0e0e974a281fd1eada9eafb7890eb5cc70414a4fbbb49dbef3c19979c24f04cb","ID":"0e0e974a281f","MemPerc":"0.03%","MemUsage":"1.16MiB / 3.904GiB","Name":"weloury-test-noport","NetIO":"84B / 126B","PIDs":"3"}`

func TestParsePSOutputMultipleLines(t *testing.T) {
	raw := []byte(fixturePSRunningWithPort + "\n" + fixturePSExitedNoPort + "\n")
	rows, err := parsePSOutput(raw)
	if err != nil {
		t.Fatalf("parsePSOutput: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0].ID != "69984edd6de7" || rows[0].State != "running" {
		t.Errorf("rows[0] = %+v", rows[0])
	}
	if rows[1].ID != "0e0e974a281f" || rows[1].State != "exited" {
		t.Errorf("rows[1] = %+v", rows[1])
	}
}

func TestParsePSOutputSkipsBlankLines(t *testing.T) {
	raw := []byte("\n" + fixturePSRunningWithPort + "\n\n")
	rows, err := parsePSOutput(raw)
	if err != nil {
		t.Fatalf("parsePSOutput: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
}

func TestParseStatsOutputKeyedByID(t *testing.T) {
	byID, err := parseStatsOutput([]byte(fixtureStatsRunning))
	if err != nil {
		t.Fatalf("parseStatsOutput: %v", err)
	}
	row, ok := byID["0e0e974a281f"]
	if !ok {
		t.Fatal("expected an entry for 0e0e974a281f")
	}
	if row.CPUPerc != "0.00%" || row.MemUsage != "1.16MiB / 3.904GiB" || row.PIDs != "3" {
		t.Errorf("row = %+v", row)
	}
}

func TestParseDockerPercent(t *testing.T) {
	got, err := parseDockerPercent("0.00%")
	if err != nil {
		t.Fatalf("parseDockerPercent: %v", err)
	}
	if got != 0 {
		t.Errorf("got %v, want 0", got)
	}

	got, err = parseDockerPercent("12.34%")
	if err != nil {
		t.Fatalf("parseDockerPercent: %v", err)
	}
	if got != 12.34 {
		t.Errorf("got %v, want 12.34", got)
	}
}

func TestParseDockerSizeUnits(t *testing.T) {
	cases := map[string]uint64{
		"84B":      84,
		"1.16MiB":  1216348,    // 1.16 * 1<<20, truncated -- computed independently in Python, not hand-derived
		"3.904GiB": 4191888080, // 3.904 * 1<<30, truncated
		"0B":       0,
	}
	for in, want := range cases {
		got, err := parseDockerSize(in)
		if err != nil {
			t.Errorf("parseDockerSize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDockerSizeUnrecognizedUnit(t *testing.T) {
	if _, err := parseDockerSize("5 furlongs"); err == nil {
		t.Error("expected an error for an unrecognized unit")
	}
}

func TestParseMemUsage(t *testing.T) {
	used, limit, err := parseMemUsage("1.16MiB / 3.904GiB")
	if err != nil {
		t.Fatalf("parseMemUsage: %v", err)
	}
	wantUsed := uint64(1216348)
	wantLimit := uint64(4191888080)
	if used != wantUsed || limit != wantLimit {
		t.Errorf("got used=%d limit=%d, want used=%d limit=%d", used, limit, wantUsed, wantLimit)
	}
}

func TestParseMemUsageMalformed(t *testing.T) {
	if _, _, err := parseMemUsage("no slash here"); err == nil {
		t.Error("expected an error for a MemUsage string with no '/'")
	}
}

func TestMergeContainerRowWithStats(t *testing.T) {
	rows, err := parsePSOutput([]byte(fixturePSExitedNoPort))
	if err != nil {
		t.Fatalf("parsePSOutput: %v", err)
	}
	statsByID, err := parseStatsOutput([]byte(fixtureStatsRunning))
	if err != nil {
		t.Fatalf("parseStatsOutput: %v", err)
	}
	stats := statsByID[rows[0].ID]

	c := mergeContainerRow(rows[0], &stats)
	if c.Name != "weloury-test-noport" {
		t.Errorf("Name = %q, want leading slash stripped: weloury-test-noport", c.Name)
	}
	if c.CPUPercent == nil || *c.CPUPercent != 0 {
		t.Errorf("CPUPercent = %v, want 0", c.CPUPercent)
	}
	if c.MemoryUsageBytes == nil || c.MemoryLimitBytes == nil {
		t.Fatal("expected memory fields to be populated")
	}
	if c.PIDCount == nil || *c.PIDCount != 3 {
		t.Errorf("PIDCount = %v, want 3", c.PIDCount)
	}
	if c.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to parse from the real docker ps timestamp format")
	}
}

func TestMergeContainerRowWithoutStats(t *testing.T) {
	rows, err := parsePSOutput([]byte(fixturePSExitedNoPort))
	if err != nil {
		t.Fatalf("parsePSOutput: %v", err)
	}
	c := mergeContainerRow(rows[0], nil)
	if c.CPUPercent != nil || c.MemoryUsageBytes != nil || c.PIDCount != nil {
		t.Errorf("expected all resource fields nil for a stopped/unmatched container, got %+v", c)
	}
	if c.State != "exited" {
		t.Errorf("State = %q, want exited", c.State)
	}
}

func TestParsePSOutputInvalidJSON(t *testing.T) {
	if _, err := parsePSOutput([]byte("not json\n")); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}
