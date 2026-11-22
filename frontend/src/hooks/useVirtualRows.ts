import { useEffect, useRef, useState, type RefObject } from "react";

interface VirtualRowsResult {
  startIndex: number;
  /** Exclusive. */
  endIndex: number;
  topSpacerPx: number;
  bottomSpacerPx: number;
}

/**
 * Fixed-row-height virtualization for a scrollable list - a `<tbody>`
 * of `<tr>`s or a plain `<div>` list of row-like children, either
 * works, since this only ever deals in row indices and pixel offsets,
 * never DOM node types. Only rows within the visible viewport (+
 * `overscan`) are returned for rendering; two spacers (`topSpacerPx` /
 * `bottomSpacerPx` - a `<tr>` for table rows, a `<div>` for anything
 * else) keep the scrollbar's size and position correct for the full
 * row count without every row actually existing in the DOM. Used by
 * `ProcessesPage.tsx` (a `<table>`) and `LogsPage.tsx` (a `<div>`
 * list) - same hook, different markup wrapping it.
 *
 * Exists because measurement (see CHANGELOG.md's Phase 7 entry) showed
 * the real cost of a large process list isn't scroll jank - scrolling
 * an already-laid-out list turned out to be near-zero-cost at every
 * size tested up to 20,000 rows - it's that mounting the list and
 * re-rendering it on every filter keystroke both scale linearly with
 * total row count, because the full filtered set gets reconciled into
 * real DOM nodes regardless of what's actually visible. Capping the
 * rendered row count decouples both costs from total N.
 *
 * Deliberately not a general-purpose windowing library: this covers
 * exactly Weloury's case (fixed-height rows, one scrollable container)
 * in a small hook rather than pulling in a dependency for it - see
 * ARCHITECTURE.md on keeping the frontend dependency surface small.
 *
 * Known limitation, not papered over: Tab only reaches rows currently
 * rendered. A keyboard-only user tabbing past the last rendered row
 * moves to the next focusable element after the list, not further
 * down it - full virtualized-list keyboard traversal would need
 * arrow-key roving-tabindex navigation (the same scope boundary noted
 * for ProcessTree's tree navigation), which isn't implemented here
 * either. In practice the filter box, not Tab, is how a keyboard user
 * narrows a list too large to Tab through either way.
 */
export function useVirtualRows(
  containerRef: RefObject<HTMLElement | null>,
  rowCount: number,
  rowHeightPx: number,
  overscan = 8,
): VirtualRowsResult {
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(0);
  const rafRef = useRef<number | null>(null);

  // Both consumers render their scroll container *conditionally* -
  // ProcessesPage shows a "Loading..." placeholder until its fetch
  // resolves, LogsPage until its backlog query returns - so on first
  // mount containerRef.current is usually null. The original version of
  // this effect bailed on that null and never retried (its deps never
  // change), which meant the scroll listener and ResizeObserver were
  // silently never attached for the entire life of the list: data
  // arrived, rows rendered, and then scrolling did nothing - the list
  // looked frozen at its first overscan slice until something else
  // remounted it (the processes page's list/tree toggle, exactly the
  // workaround the bug produced). The fix is to poll for attachment
  // with rAF - a few frames of cheap null checks - and subscribe the
  // moment the container exists. Everything below is idempotent per
  // attachment and fully torn down before the next attempt.
  useEffect(() => {
    let disposed = false;
    let attachedEl: HTMLElement | null = null;
    let observer: ResizeObserver | null = null;
    let retryRaf: number | null = null;

    function readAndSet() {
      if (!attachedEl) return;
      setScrollTop(attachedEl.scrollTop);
      setViewportHeight(attachedEl.clientHeight);
    }

    function onScroll() {
      if (rafRef.current != null) return;
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = null;
        readAndSet();
      });
    }

    function attach() {
      if (disposed) return;
      const el = containerRef.current;
      if (!el) {
        retryRaf = requestAnimationFrame(attach);
        return;
      }
      attachedEl = el;
      readAndSet();
      observer = new ResizeObserver(readAndSet);
      observer.observe(el);
      el.addEventListener("scroll", onScroll, { passive: true });
    }

    attach();
    return () => {
      disposed = true;
      if (retryRaf != null) cancelAnimationFrame(retryRaf);
      if (observer) observer.disconnect();
      if (attachedEl) attachedEl.removeEventListener("scroll", onScroll);
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
    };
  }, [containerRef]);

  // Re-measure when the row count first becomes non-zero even if the
  // scroll subscription above is already live: a growing/shrinking list
  // can change the scroll container's own height (scrollbar appearing),
  // which changes clientHeight. The ResizeObserver usually covers this,
  // but it fires only on *observed* box changes; mounting the real table
  // into a container measured while empty is exactly the transition it
  // can miss on some layouts, so an explicit re-measure here keeps the
  // visible window honest. Once measured, this no-ops.
  const hasMeasured = viewportHeight > 0;
  useEffect(() => {
    if (hasMeasured) return;
    const el = containerRef.current;
    if (!el) return;
    const id = requestAnimationFrame(() => {
      if (el.clientHeight > 0) {
        setScrollTop(el.scrollTop);
        setViewportHeight(el.clientHeight);
      }
    });
    return () => cancelAnimationFrame(id);
  }, [containerRef, hasMeasured, rowCount]);

  if (rowHeightPx <= 0 || rowCount === 0) {
    return { startIndex: 0, endIndex: rowCount, topSpacerPx: 0, bottomSpacerPx: 0 };
  }

  // rowCount is in the deps above because a growing/shrinking list can
  // change the scroll container's own height (scrollbar appearing),
  // which changes clientHeight; re-measuring on that transition keeps
  // the visible window honest. A 0 viewport before the first successful
  // measure would render only `overscan` rows - the exact bug this
  // guard's re-measure fixes - so treat an unmeasured container as "not
  // ready" and render the first slice, which the follow-up effect above
  // immediately corrects to the real viewport size.
  const effectiveViewport = viewportHeight > 0 ? viewportHeight : overscan * 2 * rowHeightPx;

  const rawStart = Math.floor(scrollTop / rowHeightPx) - overscan;
  const startIndex = Math.min(Math.max(0, rawStart), Math.max(0, rowCount - 1));
  const visibleCount = Math.ceil(effectiveViewport / rowHeightPx) + overscan * 2;
  const endIndex = Math.min(rowCount, startIndex + visibleCount);

  return {
    startIndex,
    endIndex,
    topSpacerPx: startIndex * rowHeightPx,
    bottomSpacerPx: (rowCount - endIndex) * rowHeightPx,
  };
}
