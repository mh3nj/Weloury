import type { KeyboardEvent } from "react";

/**
 * Keydown handler for elements that are clickable but not natively
 * interactive - most notably the selectable `<tr>` rows used as the
 * list half of every list/detail screen (Processes, Services, Health,
 * Automation, Containers). A native `<button>` gets Enter/Space
 * activation and page-scroll-on-Space suppression for free; a `<tr
 * onClick>` gets neither, which made every one of those rows
 * mouse-only. Pair with `tabIndex={0}` and a focus-visible ring class
 * on the same element - this helper only supplies the key handling.
 */
export function onActivateKey(onActivate: () => void) {
  return (e: KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onActivate();
    }
  };
}

/** Shared focus-visible ring classes for elements made focusable via tabIndex rather than a native interactive tag. */
export const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent";
