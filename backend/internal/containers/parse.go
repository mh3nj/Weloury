package containers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// psRow is one line of `docker ps -a --format '{{json .}}'` output -
// field names and casing matched exactly to Docker's own JSON keys
// (verified against a real running Docker 29.1.3 daemon in this
// sandbox - see linux_integration_test.go - not guessed from
// documentation alone).
type psRow struct {
	ID        string `json:"ID"`
	Names     string `json:"Names"`
	Image     string `json:"Image"`
	State     string `json:"State"`
	Status    string `json:"Status"`
	Ports     string `json:"Ports"`
	CreatedAt string `json:"CreatedAt"`
}

// dockerPSCreatedAtLayout matches Docker's own CreatedAt format, e.g.
// "2026-08-25 11:18:42 +0000 UTC" - captured verbatim from real `docker
// ps` output, not derived from documentation.
const dockerPSCreatedAtLayout = "2006-01-02 15:04:05 -0700 MST"

// parsePSOutput parses `docker ps`'s NDJSON output - one compact JSON
// object per line, per Docker's own `--format '{{json .}}'` behavior.
func parsePSOutput(raw []byte) ([]psRow, error) {
	var out []psRow
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row psRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("parse docker ps line %q: %w", line, err)
		}
		out = append(out, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan docker ps output: %w", err)
	}
	return out, nil
}

// statsRow is one line of `docker stats --no-stream --format
// '{{json .}}'` output - again, field names matched to a real captured
// daemon's output. Only running containers appear in this output at
// all; see Provider.List's doc comment.
type statsRow struct {
	ID       string `json:"ID"`
	CPUPerc  string `json:"CPUPerc"`  // e.g. "0.00%"
	MemUsage string `json:"MemUsage"` // e.g. "1.16MiB / 3.904GiB"
	PIDs     string `json:"PIDs"`     // e.g. "3"
}

// parseStatsOutput parses `docker stats`'s NDJSON output the same way
// parsePSOutput does, keyed by container ID for Provider.List to merge
// against parsePSOutput's rows.
func parseStatsOutput(raw []byte) (map[string]statsRow, error) {
	out := make(map[string]statsRow)
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row statsRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("parse docker stats line %q: %w", line, err)
		}
		out[row.ID] = row
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan docker stats output: %w", err)
	}
	return out, nil
}

// parseDockerPercent strips a trailing '%' and parses the remainder -
// docker stats' CPUPerc/MemPerc fields' shared shape.
func parseDockerPercent(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
}

// dockerSizeUnits are the binary-prefix suffixes `docker stats` uses in
// MemUsage (e.g. "1.16MiB", "3.904GiB") - verified against real output;
// Docker always uses binary (1024-based, "*iB") units here, never decimal
// ("*B" alone means bytes with no multiplier, not "10^0 bytes" as a
// distinct unit).
var dockerSizeUnits = []struct {
	suffix     string
	multiplier float64
}{
	// Longest suffixes first so e.g. "GiB" isn't matched by a hypothetical shorter prefix.
	{"TiB", 1 << 40},
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
	{"B", 1},
}

// parseDockerSize parses one side of a docker stats MemUsage field (e.g.
// "1.16MiB") into a byte count.
func parseDockerSize(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	for _, u := range dockerSizeUnits {
		if strings.HasSuffix(s, u.suffix) {
			numStr := strings.TrimSuffix(s, u.suffix)
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("parse size %q: %w", s, err)
			}
			return uint64(num * u.multiplier), nil
		}
	}
	return 0, fmt.Errorf("unrecognized size unit in %q", s)
}

// parseMemUsage splits docker stats' "USED / LIMIT" MemUsage field (e.g.
// "1.16MiB / 3.904GiB") into two byte counts.
func parseMemUsage(s string) (used, limit uint64, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("malformed MemUsage %q: expected \"USED / LIMIT\"", s)
	}
	used, err = parseDockerSize(parts[0])
	if err != nil {
		return 0, 0, err
	}
	limit, err = parseDockerSize(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return used, limit, nil
}

// mergeContainerRow combines one psRow with its matching statsRow (if
// any - see Provider.List's doc comment on why a stopped container has
// none) into the public Container type. Parsing failures on the stats
// side (a malformed percentage or size, which would indicate Docker
// changed its output format) degrade to leaving that one numeric field
// unset rather than failing the whole row - a partially-populated
// Container is more useful than none at all for what's otherwise a
// successfully-listed container.
func mergeContainerRow(row psRow, stats *statsRow) Container {
	c := Container{
		ID:     row.ID,
		Name:   strings.TrimPrefix(row.Names, "/"),
		Image:  row.Image,
		State:  row.State,
		Status: row.Status,
		Ports:  row.Ports,
	}
	if t, err := time.Parse(dockerPSCreatedAtLayout, row.CreatedAt); err == nil {
		c.CreatedAt = t
	}

	if stats == nil {
		return c
	}
	if pct, err := parseDockerPercent(stats.CPUPerc); err == nil {
		c.CPUPercent = &pct
	}
	if used, limit, err := parseMemUsage(stats.MemUsage); err == nil {
		c.MemoryUsageBytes = &used
		c.MemoryLimitBytes = &limit
	}
	if pids, err := strconv.Atoi(strings.TrimSpace(stats.PIDs)); err == nil {
		c.PIDCount = &pids
	}
	return c
}
