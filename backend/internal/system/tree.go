package system

import "sort"

// BuildProcessTree arranges a flat process list into parent/child trees
// by PPID. It is pure data transformation over data ListProcesses already
// returned - no OS-specific code, no build tag, and no dependency on
// which platform gathered the input, which is why it lives here rather
// than in any provider_<os>.go file.
//
// A process whose PPID doesn't appear as another process's PID in the
// input (the most common case: PID 1 / launchd / the Windows System
// process, whose own parent is either absent or already exited) becomes
// a root of the returned forest - plural, deliberately: a flat process
// list taken at one instant essentially always contains more than one
// such root (kernel threads with PPID 0 on Linux, for example), so this
// returns a slice of roots rather than assuming exactly one.
//
// Processes are returned children-before-siblings in each level, sorted
// by PID, for a stable, deterministic tree the UI can render without its
// own additional sorting.
func BuildProcessTree(processes []ProcessInfo) []*ProcessTreeNode {
	nodes := make(map[int32]*ProcessTreeNode, len(processes))
	for _, p := range processes {
		nodes[p.PID] = &ProcessTreeNode{ProcessInfo: p}
	}

	var roots []*ProcessTreeNode
	for _, p := range processes {
		node := nodes[p.PID]
		parent, hasParent := nodes[p.PPID]
		// A process is only ever a "child" of a PPID that is itself
		// present in this same snapshot and isn't itself (a PPID of 0
		// pointing at a PID-0 entry that doesn't exist is the common
		// case that lands here, not a real cycle).
		if hasParent && p.PPID != p.PID {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}

	sortTreeLevel(roots)
	return roots
}

func sortTreeLevel(nodes []*ProcessTreeNode) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].PID < nodes[j].PID })
	for _, n := range nodes {
		sortTreeLevel(n.Children)
	}
}
