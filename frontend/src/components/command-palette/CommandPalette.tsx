import { useEffect, useMemo, useRef, useState } from "react";
import { NAV_ITEMS, type ViewId } from "@/types/nav";
import { useDialogKeyboard } from "@/hooks/useDialogKeyboard";

interface CommandPaletteProps {
  open: boolean;
  onClose: () => void;
  onNavigate: (view: ViewId) => void;
}

/**
 * Keyboard-first navigation (Cmd/Ctrl+K to open, arrows + Enter to select,
 * Escape to close). Phase 1 wires only navigation; later phases extend
 * the same command list with process/service search and diagnostic
 * actions (see spec section 22) without changing this component's shape.
 */
export function CommandPalette({ open, onClose, onNavigate }: CommandPaletteProps) {
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const containerRef = useRef<HTMLDivElement>(null);

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return NAV_ITEMS;
    return NAV_ITEMS.filter((item) => item.label.toLowerCase().includes(q));
  }, [query]);

  useEffect(() => {
    if (open) {
      setQuery("");
      setActiveIndex(0);
    }
  }, [open]);

  useEffect(() => {
    setActiveIndex(0);
  }, [query]);

  // Escape-to-close, Tab trapping, and initial/returned focus are handled
  // by useDialogKeyboard, shared with every other dialog in Weloury. This
  // effect only owns the palette's own navigation keys.
  useEffect(() => {
    if (!open) return;

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIndex((i) => Math.min(i + 1, results.length - 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIndex((i) => Math.max(i - 1, 0));
      } else if (e.key === "Enter") {
        const item = results[activeIndex];
        if (item) {
          onNavigate(item.id);
          onClose();
        }
      }
    }

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, results, activeIndex, onNavigate, onClose]);

  useDialogKeyboard(open, containerRef, onClose);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/50 pt-32" onClick={onClose}>
      <div
        ref={containerRef}
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="w-full max-w-lg overflow-hidden rounded-lg border border-border-strong bg-surface-2 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Jump to..."
          className="w-full border-b border-border bg-transparent px-4 py-3 text-sm text-text-primary placeholder:text-text-muted focus:outline-none"
        />
        <ul className="max-h-80 overflow-y-auto py-1" role="listbox">
          {results.length === 0 && <li className="px-4 py-6 text-center text-sm text-text-muted">No matches</li>}
          {results.map((item, index) => (
            <li key={item.id} role="option" aria-selected={index === activeIndex}>
              <button
                onClick={() => {
                  onNavigate(item.id);
                  onClose();
                }}
                onMouseEnter={() => setActiveIndex(index)}
                className={`flex w-full items-center justify-between px-4 py-2 text-left text-sm ${
                  index === activeIndex ? "bg-accent/15 text-text-primary" : "text-text-secondary"
                }`}
              >
                <span>{item.label}</span>
                {item.phase > 1 && <span className="text-xs text-text-muted">Phase {item.phase}</span>}
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
