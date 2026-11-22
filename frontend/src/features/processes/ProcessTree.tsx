import { useState } from "react";
import { StatusDot } from "@/components/ui/StatusDot";
import { formatBytes } from "@/lib/format";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";
import type { ProcessTreeNode } from "@/types/system";

const STATUS_MAP: Record<string, "healthy" | "degraded" | "unhealthy" | "unknown"> = {
  running: "healthy",
  sleeping: "unknown",
  idle: "unknown",
  disk_sleep: "degraded",
  stopped: "degraded",
  zombie: "unhealthy",
  dead: "unhealthy",
};

interface ProcessTreeProps {
  roots: ProcessTreeNode[];
  onSelect: (pid: number) => void;
  selectedPid?: number;
}

export function ProcessTree({ roots, onSelect, selectedPid }: ProcessTreeProps) {
  return (
    <ul className="p-2 text-sm" role="tree" aria-label="Process tree">
      {roots.map((node) => (
        <TreeRow key={node.pid} node={node} depth={0} onSelect={onSelect} selectedPid={selectedPid} />
      ))}
    </ul>
  );
}

function TreeRow({
  node,
  depth,
  onSelect,
  selectedPid,
}: {
  node: ProcessTreeNode;
  depth: number;
  onSelect: (pid: number) => void;
  selectedPid?: number;
}) {
  const [expanded, setExpanded] = useState(depth < 2); // shallow levels open by default
  const hasChildren = (node.children?.length ?? 0) > 0;

  return (
    <li
      role="treeitem"
      tabIndex={0}
      aria-selected={selectedPid === node.pid}
      aria-expanded={hasChildren ? expanded : undefined}
      onKeyDown={onActivateKey(() => onSelect(node.pid))}
      className={FOCUS_RING}
    >
      <div
        className={`flex cursor-pointer items-center gap-2 rounded-md px-2 py-1 hover:bg-surface-2 ${
          selectedPid === node.pid ? "bg-accent/15" : ""
        }`}
        style={{ paddingLeft: `${depth * 20 + 8}px` }}
        onClick={() => onSelect(node.pid)}
      >
        <button
          onClick={(e) => {
            e.stopPropagation();
            setExpanded((v) => !v);
          }}
          className={`w-4 text-center text-text-muted ${hasChildren ? "" : "invisible"}`}
          aria-label={expanded ? "Collapse" : "Expand"}
        >
          {expanded ? "▾" : "▸"}
        </button>
        <span className="tabular w-16 text-text-muted">{node.pid}</span>
        <span className="flex-1 truncate text-text-primary">{node.name || "-"}</span>
        <StatusDot status={STATUS_MAP[node.status] ?? "unknown"} />
        <span className="tabular w-16 text-right text-text-secondary">
          {node.memory_bytes ? formatBytes(node.memory_bytes) : "-"}
        </span>
      </div>
      {hasChildren && expanded && (
        <ul role="group">
          {node.children!.map((child) => (
            <TreeRow key={child.pid} node={child} depth={depth + 1} onSelect={onSelect} selectedPid={selectedPid} />
          ))}
        </ul>
      )}
    </li>
  );
}
