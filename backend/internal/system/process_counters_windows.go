//go:build windows

package system

import (
	"context"
	"strings"
)

// processCountersSnapshot is the parsed Win32_PerfFormattedData_PerfProc_
// Process table: one row per running process (plus a "_Total" instance),
// keyed by PID. PercentProcessorTime is pre-formatted CPU% of ONE core;
// Task Manager's Processes-tab CPU column is share-of-whole-machine, so
// the per-core figure divides by logical core count - that conversion
// happens once, here, at the single place that knows both conventions.
type processCountersSnapshot struct {
	byPID map[int32]processCounterRow
}

type processCounterRow struct {
	name          string
	cpuPercent    float64 // whole-machine share, 0-100
	ioBytesPerSec float64
}

// fetchProcessCounters runs one PowerShell query for the two counter
// columns the task-manager-grade process list needs. The class includes
// a "_Total" instance whose IDProcess is 0 - skipped, since the process
// listing never carries a meaningful PID 0 row to enrich.
func (p *windowsProvider) fetchProcessCounters(ctx context.Context) processCountersSnapshot {
	snap := processCountersSnapshot{byPID: make(map[int32]processCounterRow, 300)}
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_PerfFormattedData_PerfProc_Process | Select-Object IDProcess,Name,PercentProcessorTime,IODataBytesPersec | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return snap
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return snap
	}
	for _, row := range rows {
		pid := int32(parseIntField(row, "idprocess"))
		if pid == 0 {
			continue // the _Total instance
		}
		snap.byPID[pid] = processCounterRow{
			name:          strings.TrimSpace(row["name"]),
			cpuPercent:    wholeMachineCPUShare(parseFloatField(row, "percentprocessortime")),
			ioBytesPerSec: parseFloatField(row, "iodatabytespersec"),
		}
	}
	return snap
}

// wholeMachineCPUShare converts the Windows counter convention (percent
// of one processor) into the Task Manager Processes-tab convention
// (percent of the whole machine): divide by logical core count, clamp
// to 100. taskManagerCPUDivisor exists so the semantic hinge is greppable.
func wholeMachineCPUShare(perCorePercent float64) float64 {
	cores := taskManagerCPUDivisor()
	if cores <= 1 {
		return clampPercent(perCorePercent)
	}
	share := perCorePercent / float64(cores)
	if share > 100 {
		share = 100
	}
	return share
}

func taskManagerCPUDivisor() int { return numLogicalCores() }

// enrichProcessList fills CPUPercent/DiskIOBytesPerSec from one bulk
// counter query. Processes missing from the counter table (a process
// that exited between the two queries, or a protected one) keep nil
// fields - "not measured this pass", exactly the pointer contract.
func (p *windowsProvider) enrichProcessList(ctx context.Context, procs []ProcessInfo) {
	snap := p.fetchProcessCounters(ctx)
	for i := range procs {
		if row, ok := snap.byPID[procs[i].PID]; ok {
			cpu := row.cpuPercent
			io := row.ioBytesPerSec
			procs[i].CPUPercent = &cpu
			procs[i].DiskIOBytesPerSec = &io
		}
	}
}
