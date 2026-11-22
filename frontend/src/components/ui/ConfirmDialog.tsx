import { useRef } from "react";
import { Button } from "./Button";
import { useDialogKeyboard } from "@/hooks/useDialogKeyboard";

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel?: string;
  destructive?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

/**
 * A focus-managed confirmation modal for dangerous or irreversible
 * actions (process termination, service stop, destructive automation
 * actions, ...). Every such action in Weloury must route through this
 * component or an equivalent - see spec section 6/11, "never allow
 * accidental termination through a single careless click." Not exercised
 * by any Phase 1 feature yet; it exists now because it's shared UI
 * infrastructure the process/service/automation features (Phase 3/5)
 * depend on, and is cheap to get right once rather than reinvented per
 * feature.
 */
export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = "Confirm",
  destructive = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const confirmRef = useRef<HTMLButtonElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useDialogKeyboard(open, containerRef, onCancel, confirmRef);

  if (!open) return null;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="confirm-dialog-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    >
      <div ref={containerRef} className="w-full max-w-sm rounded-lg border border-border-strong bg-surface-2 p-5 shadow-xl">
        <h2 id="confirm-dialog-title" className="text-sm font-semibold text-text-primary">
          {title}
        </h2>
        <p className="mt-2 text-sm text-text-secondary">{description}</p>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button ref={confirmRef} variant={destructive ? "danger" : "primary"} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
