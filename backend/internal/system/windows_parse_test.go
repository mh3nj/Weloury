//go:build windows

package system

import (
	"testing"
	"time"
)

const fixtureProcessCSV = `"ProcessId","ParentProcessId","Name","WorkingSetSize"
"4","0","System","131072"
"1234","4","explorer.exe","47185920"
`

func TestParseCIMCsv(t *testing.T) {
	rows, err := parseCIMCsv(fixtureProcessCSV)
	if err != nil {
		t.Fatalf("parseCIMCsv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[1]["name"] != "explorer.exe" {
		t.Fatalf(`rows[1]["name"] = %q, want "explorer.exe"`, rows[1]["name"])
	}
	if rows[1]["processid"] != "1234" {
		t.Fatalf(`rows[1]["processid"] = %q, want "1234"`, rows[1]["processid"])
	}
}

func TestParseCIMCsvEmptyInput(t *testing.T) {
	rows, err := parseCIMCsv("")
	if err != nil {
		t.Fatalf("parseCIMCsv on empty input: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows for empty input, want 0", len(rows))
	}
}

func TestParseUintField(t *testing.T) {
	row := map[string]string{"workingsetsize": "47185920", "empty": ""}
	if got := parseUintField(row, "workingsetsize"); got != 47185920 {
		t.Fatalf("got %d, want 47185920", got)
	}
	if got := parseUintField(row, "empty"); got != 0 {
		t.Fatalf("got %d for empty field, want 0", got)
	}
	if got := parseUintField(row, "missing"); got != 0 {
		t.Fatalf("got %d for missing field, want 0", got)
	}
}

func TestParseIntField(t *testing.T) {
	row := map[string]string{"numberofcores": "8"}
	if got := parseIntField(row, "numberofcores"); got != 8 {
		t.Fatalf("got %d, want 8", got)
	}
	if got := parseIntField(row, "missing"); got != 0 {
		t.Fatalf("got %d for missing field, want 0", got)
	}
}

func TestParseWMIDateTime(t *testing.T) {
	got, err := parseWMIDateTime("20240115103045.123456-300")
	if err != nil {
		t.Fatalf("parseWMIDateTime: %v", err)
	}
	// -300 minutes = 5 hours behind UTC, so the UTC instant is 5 hours
	// later than the local wall-clock time encoded in the string.
	wantUTC := time.Date(2024, 1, 15, 15, 30, 45, 123456000, time.UTC)
	if !got.UTC().Equal(wantUTC) {
		t.Fatalf("got UTC %v, want %v", got.UTC(), wantUTC)
	}
}

func TestParseWMIDateTimePositiveOffset(t *testing.T) {
	got, err := parseWMIDateTime("20240115103045.000000+060")
	if err != nil {
		t.Fatalf("parseWMIDateTime: %v", err)
	}
	wantUTC := time.Date(2024, 1, 15, 9, 30, 45, 0, time.UTC)
	if !got.UTC().Equal(wantUTC) {
		t.Fatalf("got UTC %v, want %v", got.UTC(), wantUTC)
	}
}

func TestParseWMIDateTimeRejectsTooShortInput(t *testing.T) {
	if _, err := parseWMIDateTime("2024011510"); err == nil {
		t.Fatal("expected an error for a too-short WMI datetime string")
	}
}

func TestParseWMIDateTimeRejectsMissingSign(t *testing.T) {
	if _, err := parseWMIDateTime("20240115103045.123456300"); err == nil {
		t.Fatal("expected an error when the +/- offset sign is missing")
	}
}

func TestNormalizeWin32ServiceState(t *testing.T) {
	cases := map[string]string{"Running": "running", "Stopped": "stopped", "Paused": "unknown", "": "unknown"}
	for input, want := range cases {
		if got := normalizeWin32ServiceState(input); got != want {
			t.Errorf("normalizeWin32ServiceState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeWin32ServiceStartMode(t *testing.T) {
	cases := map[string]string{"Auto": "enabled", "Manual": "manual", "Disabled": "disabled", "": "unknown"}
	for input, want := range cases {
		if got := normalizeWin32ServiceStartMode(input); got != want {
			t.Errorf("normalizeWin32ServiceStartMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEscapePowerShellSingleQuoted(t *testing.T) {
	if got := escapePowerShellSingleQuoted(`O'Brien's Service`); got != `O''Brien''s Service` {
		t.Fatalf("got %q", got)
	}
	if got := escapePowerShellSingleQuoted("NoQuotesHere"); got != "NoQuotesHere" {
		t.Fatalf("got %q, want unchanged", got)
	}
}

const fixtureServiceCSV = `"Name","DisplayName","Description","State","StartMode","ProcessId"
"Spooler","Print Spooler","Manages print jobs","Running","Auto","1876"
`

func TestServiceInfoFromCIMRow(t *testing.T) {
	rows, err := parseCIMCsv(fixtureServiceCSV)
	if err != nil {
		t.Fatalf("parseCIMCsv: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	info := serviceInfoFromCIMRow(rows[0])
	if info.Name != "Spooler" || info.DisplayName != "Print Spooler" {
		t.Fatalf("info = %+v", info)
	}
	if info.State != "running" || info.StartupMode != "enabled" {
		t.Fatalf("info.State=%q info.StartupMode=%q, want running/enabled", info.State, info.StartupMode)
	}
	if info.PID != 1876 {
		t.Fatalf("info.PID = %d, want 1876", info.PID)
	}
}
