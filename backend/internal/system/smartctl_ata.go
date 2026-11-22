package system

// clampFloatPercent bounds a percentage reading to [0, 100]. Counter
// sources occasionally report transient out-of-range values (a spike past
// 100 while a boost clock settles); clamping keeps one weird sample from
// stretching a chart's scale. No build tag: needed by the nvidia-smi path
// on every platform (the windows_parse.go original is windows-only and
// takes the CIM row-map shape instead).
func clampFloatPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
