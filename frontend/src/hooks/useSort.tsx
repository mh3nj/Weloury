import { useMemo, useState, type ReactNode } from "react";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";

/**
 * Shared sortable-column machinery for every list in the app (the
 * "click a column header to sort by it" ask: name, memory, CPU, size,
 * status - everything that makes sense per table). Sort state is
 * `{ key, dir }`; columns declare a sort key and a value extractor per
 * row, and useSort computes the sorted view with a stable tie-break on
 * the row's own index so equal values never jitter between refreshes.
 *
 * Sorting happens on the already-filtered array (filter first, then
 * sort - matching every file-manager convention) and recomputes only
 * when rows, the active key, or the direction change.
 */
export type SortDir = "asc" | "desc";

export interface SortState<K extends string> {
  key: K | null;
  dir: SortDir;
}

export function useSort<K extends string, T>(
  rows: T[],
  extractors: Record<K, (row: T) => number | string>,
  initialKey: K | null = null,
  initialDir: SortDir = "asc",
): { sorted: T[]; sort: SortState<K>; toggleSort: (key: K) => void } {
  const [sort, setSort] = useState<SortState<K>>({ key: initialKey, dir: initialDir });

  function toggleSort(key: K) {
    setSort((prev) => {
      if (prev.key !== key) return { key, dir: "asc" };
      if (prev.dir === "asc") return { key, dir: "desc" };
      // Third click clears the sort (back to natural order) - the
      // three-state cycle keeps big listings reversible without a
      // separate "clear" control.
      return { key: null, dir: "asc" };
    });
  }

  const sorted = useMemo(() => {
    if (sort.key == null) return rows;
    const extract = extractors[sort.key];
    if (!extract) return rows;
    const indexed = rows.map((row, i) => ({ row, i, value: extract(row) }));
    const dir = sort.dir === "asc" ? 1 : -1;
    indexed.sort((a, b) => {
      const av = a.value;
      const bv = b.value;
      let cmp: number;
      if (typeof av === "number" && typeof bv === "number") {
        cmp = av - bv;
      } else {
        cmp = String(av).localeCompare(String(bv), undefined, { numeric: true, sensitivity: "base" });
      }
      if (cmp !== 0) return cmp * dir;
      // Stable tie-break on the row's pre-sort position so equal values
      // never jitter between refreshes.
      return a.i - b.i;
    });
    return indexed.map((x) => x.row);
    // extractors is intentionally not a dep: callers pass an inline
    // object every render; the extractors' closure inputs are exactly
    // `rows`, which IS a dep. Rebuilding the memo on the object identity
    // would defeat it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows, sort.key, sort.dir]);

  return { sorted, sort, toggleSort };
}

/**
 * One sortable `<th>`: a real button (keyboard-activatable), showing the
 * current direction with an arrow and the neutral state with a dim
 * two-way glyph. Spans the header's own styling via className.
 */
export function SortableTh<K extends string>({
  label,
  sortKey,
  sort,
  onToggle,
  align = "left",
  className = "",
}: {
  label: ReactNode;
  sortKey: K;
  sort: SortState<K>;
  onToggle: (key: K) => void;
  align?: "left" | "right";
  className?: string;
}) {
  const active = sort.key === sortKey;
  const arrow = active ? (sort.dir === "asc" ? "▲" : "▼") : "↕";
  return (
    <th
      className={`px-4 py-2 font-medium ${align === "right" ? "text-right" : ""} ${className}`}
      aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
    >
      <button
        onClick={() => onToggle(sortKey)}
        onKeyDown={onActivateKey(() => onToggle(sortKey))}
        className={`inline-flex items-center gap-1 rounded ${FOCUS_RING} hover:text-text-primary ${
          active ? "text-text-primary" : ""
        }`}
        title={`Sort by ${typeof label === "string" ? label : "column"}`}
      >
        <span>{label}</span>
        <span aria-hidden="true" className={`text-[10px] ${active ? "" : "opacity-40"}`}>
          {arrow}
        </span>
      </button>
    </th>
  );
}
