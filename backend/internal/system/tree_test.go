package system

import "testing"

func TestBuildProcessTreeSimpleHierarchy(t *testing.T) {
	procs := []ProcessInfo{
		{PID: 1, PPID: 0, Name: "init"},
		{PID: 10, PPID: 1, Name: "shell"},
		{PID: 20, PPID: 10, Name: "editor"},
		{PID: 11, PPID: 1, Name: "daemon"},
	}

	roots := BuildProcessTree(procs)
	if len(roots) != 1 {
		t.Fatalf("got %d roots, want 1 (only PID 1 has no parent in the snapshot): %+v", len(roots), roots)
	}
	init := roots[0]
	if init.PID != 1 {
		t.Fatalf("root PID = %d, want 1", init.PID)
	}
	if len(init.Children) != 2 {
		t.Fatalf("init has %d children, want 2", len(init.Children))
	}
	// Sorted by PID: shell(10) before daemon(11).
	if init.Children[0].PID != 10 || init.Children[1].PID != 11 {
		t.Fatalf("children not sorted by PID: %+v", init.Children)
	}
	shell := init.Children[0]
	if len(shell.Children) != 1 || shell.Children[0].PID != 20 {
		t.Fatalf("shell's children = %+v, want exactly editor(20)", shell.Children)
	}
}

func TestBuildProcessTreeMultipleRoots(t *testing.T) {
	// A flat snapshot commonly has several PPIDs (0, or exited parents)
	// that don't correspond to any process in the same snapshot - every
	// one of those processes must become its own root, not be dropped.
	procs := []ProcessInfo{
		{PID: 1, PPID: 0, Name: "init"},
		{PID: 2, PPID: 0, Name: "kthreadd"},
		{PID: 100, PPID: 99, Name: "orphan"}, // PPID 99 doesn't exist in this snapshot
	}

	roots := BuildProcessTree(procs)
	if len(roots) != 3 {
		t.Fatalf("got %d roots, want 3: %+v", len(roots), roots)
	}
}

func TestBuildProcessTreeSelfParentDoesNotInfiniteLoop(t *testing.T) {
	// Defensive: a process reporting itself as its own parent (shouldn't
	// happen from any real OS, but input is input) must not be treated as
	// a child of itself.
	procs := []ProcessInfo{{PID: 5, PPID: 5, Name: "weird"}}

	roots := BuildProcessTree(procs)
	if len(roots) != 1 || roots[0].PID != 5 {
		t.Fatalf("roots = %+v, want a single root with PID 5", roots)
	}
	if len(roots[0].Children) != 0 {
		t.Fatalf("expected no children, got %+v", roots[0].Children)
	}
}

func TestBuildProcessTreeEmptyInput(t *testing.T) {
	roots := BuildProcessTree(nil)
	if len(roots) != 0 {
		t.Fatalf("got %d roots for empty input, want 0", len(roots))
	}
}

func TestBuildProcessTreeIsDeterministic(t *testing.T) {
	procs := []ProcessInfo{
		{PID: 3, PPID: 1, Name: "c"},
		{PID: 1, PPID: 0, Name: "a"},
		{PID: 2, PPID: 1, Name: "b"},
	}
	first := BuildProcessTree(procs)
	second := BuildProcessTree(procs)

	if len(first) != len(second) || first[0].PID != second[0].PID {
		t.Fatal("BuildProcessTree produced different results for identical input")
	}
	if first[0].Children[0].PID != 2 || first[0].Children[1].PID != 3 {
		t.Fatalf("children not deterministically sorted: %+v", first[0].Children)
	}
}
