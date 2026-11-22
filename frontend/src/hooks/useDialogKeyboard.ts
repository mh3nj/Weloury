import { useEffect, type RefObject } from "react";

const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

function focusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
    (el) => el.offsetParent !== null, // skip hidden/collapsed elements
  );
}

/**
 * Shared keyboard/focus behavior for every modal overlay in Weloury
 * (ConfirmDialog, the automation/health/schedule form dialogs, the
 * command palette):
 *
 *  - Escape calls `onClose`.
 *  - Tab / Shift+Tab cycle focus within `containerRef` instead of
 *    escaping into the page behind the overlay - spec section 20/22's
 *    "keyboard navigation," and the WCAG 2.1 modal-dialog expectation
 *    that focus is trapped *inside* an open dialog (distinct from the
 *    no-keyboard-trap success criterion, which is about *escaping* a
 *    trap you didn't intend - here the trap is intentional and Escape
 *    always breaks it).
 *  - On open, focus moves to `initialFocusRef` if given, otherwise the
 *    first focusable element inside the container.
 *  - On close, focus returns to whatever element had it before the
 *    dialog opened (typically the button that triggered it).
 *
 * Each dialog previously reimplemented an open/Escape/initial-focus
 * effect independently; none of them trapped Tab. This consolidates
 * that behavior in one place so every dialog gets the same, correct
 * keyboard handling.
 */
export function useDialogKeyboard(
  open: boolean,
  containerRef: RefObject<HTMLElement | null>,
  onClose: () => void,
  initialFocusRef?: RefObject<HTMLElement | null>,
) {
  useEffect(() => {
    if (!open) return;

    const previouslyFocused = document.activeElement as HTMLElement | null;
    const initial = initialFocusRef?.current ?? (containerRef.current && focusableElements(containerRef.current)[0]);
    initial?.focus();

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        onClose();
        return;
      }
      if (e.key !== "Tab") return;

      const container = containerRef.current;
      if (!container) return;
      const focusable = focusableElements(container);
      if (focusable.length === 0) return;

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;

      if (e.shiftKey) {
        if (active === first || !container.contains(active)) {
          e.preventDefault();
          last.focus();
        }
      } else if (active === last || !container.contains(active)) {
        e.preventDefault();
        first.focus();
      }
    }

    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      previouslyFocused?.focus();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- containerRef/initialFocusRef are refs; identity is stable and doesn't need to retrigger the effect.
  }, [open, onClose]);
}
