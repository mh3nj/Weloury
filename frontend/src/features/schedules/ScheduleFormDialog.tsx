import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@/components/ui/Button";
import { useDialogKeyboard } from "@/hooks/useDialogKeyboard";
import type { ScheduleInput, ScheduledTask, ScheduleType } from "@/types/automation";

interface ScheduleFormDialogProps {
  open: boolean;
  existing?: ScheduledTask;
  onCancel: () => void;
  onSubmit: (input: ScheduleInput) => Promise<void>;
}

/** Converts an ISO/RFC3339 instant to the local value <input type="datetime-local"> expects. */
function toLocalInputValue(iso: string): string {
  const d = new Date(iso);
  const offsetMs = d.getTimezoneOffset() * 60_000;
  return new Date(d.getTime() - offsetMs).toISOString().slice(0, 16);
}

/** Converts a datetime-local input's value (interpreted as local time by `new Date`) to RFC3339 with an explicit offset - see scheduler.Task's doc comment on why RunAt must carry one. */
function toRFC3339(localValue: string): string {
  return new Date(localValue).toISOString();
}

const DEFAULT_INTERVAL_SECONDS = 300;

export function ScheduleFormDialog({ open, existing, onCancel, onSubmit }: ScheduleFormDialogProps) {
  const [name, setName] = useState("");
  const [type, setType] = useState<ScheduleType>("once");
  const [runAtLocal, setRunAtLocal] = useState("");
  const [intervalSeconds, setIntervalSeconds] = useState(DEFAULT_INTERVAL_SECONDS);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const nameRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setSubmitting(false);
    if (existing) {
      setName(existing.name);
      setType(existing.type);
      setRunAtLocal(existing.run_at ? toLocalInputValue(existing.run_at) : "");
      setIntervalSeconds(existing.interval_seconds ?? DEFAULT_INTERVAL_SECONDS);
    } else {
      setName("");
      setType("once");
      setRunAtLocal(toLocalInputValue(new Date(Date.now() + 60 * 60 * 1000).toISOString()));
      setIntervalSeconds(DEFAULT_INTERVAL_SECONDS);
    }
  }, [open, existing]);

  useDialogKeyboard(open, containerRef, onCancel, nameRef);

  if (!open) return null;

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit({
        name,
        type,
        run_at: type === "once" ? toRFC3339(runAtLocal) : undefined,
        interval_seconds: type === "interval" ? intervalSeconds : undefined,
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setSubmitting(false);
    }
  }

  return (
    <div
      ref={containerRef}
      role="dialog"
      aria-modal="true"
      aria-labelledby="schedule-form-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    >
      <form
        onSubmit={handleSubmit}
        className="flex max-h-[85vh] w-full max-w-md flex-col rounded-lg border border-border-strong bg-surface-2 shadow-xl"
      >
        <div className="border-b border-border px-5 py-4">
          <h2 id="schedule-form-title" className="text-sm font-semibold text-text-primary">
            {existing ? "Edit scheduled task" : "New scheduled task"}
          </h2>
        </div>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          <div className="flex flex-col gap-4 text-sm">
            <Field label="Name">
              <input
                ref={nameRef}
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. nightly cleanup"
                className={inputClass}
              />
            </Field>

            <Field label="Type">
              <select value={type} onChange={(e) => setType(e.target.value as ScheduleType)} className={inputClass}>
                <option value="once">Once, at a specific time</option>
                <option value="interval">Repeating, on an interval</option>
              </select>
            </Field>

            {type === "once" && (
              <Field label="Run at">
                <input
                  required
                  type="datetime-local"
                  value={runAtLocal}
                  onChange={(e) => setRunAtLocal(e.target.value)}
                  className={inputClass}
                />
              </Field>
            )}

            {type === "interval" && (
              <Field label="Every (seconds)">
                <input
                  required
                  type="number"
                  min={10}
                  value={intervalSeconds}
                  onChange={(e) => setIntervalSeconds(Number(e.target.value))}
                  className={inputClass}
                />
                <p className="mt-1 text-xs text-text-muted">
                  Fires this many seconds after the last run - not a fixed wall-clock time of day.
                </p>
              </Field>
            )}

            {error && <p className="text-xs text-status-unhealthy">{error}</p>}
          </div>
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-5 py-4">
          <Button type="button" variant="ghost" onClick={onCancel} disabled={submitting}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {submitting ? "Saving..." : existing ? "Save changes" : "Create task"}
          </Button>
        </div>
      </form>
    </div>
  );
}

const inputClass =
  "w-full rounded-md border border-border bg-surface-1 px-2.5 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:outline-none focus:border-border-strong";

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs text-text-muted">{label}</span>
      {children}
    </label>
  );
}
