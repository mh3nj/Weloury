import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@/components/ui/Button";
import { useDialogKeyboard } from "@/hooks/useDialogKeyboard";
import { CHECK_TYPE_META, CHECK_TYPE_ORDER } from "./checkTypeMeta";
import type { HealthCheckInput, HealthCheckStatus, HealthCheckType } from "@/types/observability";

interface HealthCheckFormDialogProps {
  open: boolean;
  /** Present when editing an existing check; absent when creating a new one. */
  existing?: HealthCheckStatus;
  onCancel: () => void;
  onSubmit: (input: HealthCheckInput) => Promise<void>;
}

const DEFAULT_INTERVAL_SECONDS = 30;
const DEFAULT_TIMEOUT_SECONDS = 5;

/**
 * Create/edit form for a health check (spec section 9). CommandArgs is
 * edited as one argument per line rather than a single shell-like string
 * - deliberately, since backend/internal/health/checkers.go's
 * checkCommand never runs these through a shell (see SECURITY.md);
 * offering a single text field a user might reasonably expect to support
 * quoting or `&&` would misrepresent what actually happens to it.
 */
export function HealthCheckFormDialog({ open, existing, onCancel, onSubmit }: HealthCheckFormDialogProps) {
  const [name, setName] = useState("");
  const [type, setType] = useState<HealthCheckType>("process_exists");
  const [target, setTarget] = useState("");
  const [commandText, setCommandText] = useState("");
  const [threshold, setThreshold] = useState(90);
  const [intervalSeconds, setIntervalSeconds] = useState(DEFAULT_INTERVAL_SECONDS);
  const [timeoutSeconds, setTimeoutSeconds] = useState(DEFAULT_TIMEOUT_SECONDS);
  const [maxRetries, setMaxRetries] = useState(0);
  const [retryDelaySeconds, setRetryDelaySeconds] = useState(0);
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
      setTarget(existing.target ?? "");
      setCommandText((existing.command_args ?? []).join("\n"));
      setThreshold(existing.threshold ?? 90);
      setIntervalSeconds(existing.interval_seconds);
      setTimeoutSeconds(existing.timeout_seconds);
      setMaxRetries(existing.retry.max_retries);
      setRetryDelaySeconds(existing.retry.retry_delay_seconds);
    } else {
      setName("");
      setType("process_exists");
      setTarget("");
      setCommandText("");
      setThreshold(90);
      setIntervalSeconds(DEFAULT_INTERVAL_SECONDS);
      setTimeoutSeconds(DEFAULT_TIMEOUT_SECONDS);
      setMaxRetries(0);
      setRetryDelaySeconds(0);
    }
  }, [open, existing]);

  useDialogKeyboard(open, containerRef, onCancel, nameRef);

  if (!open) return null;

  const meta = CHECK_TYPE_META[type];

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit({
        name,
        type,
        target: meta.needsTarget ? target : undefined,
        command_args: meta.needsCommand
          ? commandText
              .split("\n")
              .map((line) => line.trim())
              .filter((line) => line.length > 0)
          : undefined,
        threshold: meta.needsThreshold ? threshold : undefined,
        interval_seconds: intervalSeconds,
        timeout_seconds: timeoutSeconds,
        retry: { max_retries: maxRetries, retry_delay_seconds: retryDelaySeconds },
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
      aria-labelledby="health-form-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    >
      <form
        onSubmit={handleSubmit}
        className="flex max-h-[85vh] w-full max-w-md flex-col rounded-lg border border-border-strong bg-surface-2 shadow-xl"
      >
        <div className="border-b border-border px-5 py-4">
          <h2 id="health-form-title" className="text-sm font-semibold text-text-primary">
            {existing ? "Edit health check" : "New health check"}
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
                placeholder="e.g. API health"
                className={inputClass}
              />
            </Field>

            <Field label="Type">
              <select
                value={type}
                onChange={(e) => setType(e.target.value as HealthCheckType)}
                disabled={!!existing}
                className={inputClass}
              >
                {CHECK_TYPE_ORDER.map((t) => (
                  <option key={t} value={t}>
                    {CHECK_TYPE_META[t].label}
                  </option>
                ))}
              </select>
              {existing && (
                <p className="mt-1 text-xs text-text-muted">
                  Type can't be changed after creation - delete and recreate instead.
                </p>
              )}
            </Field>

            {meta.needsTarget && (
              <Field label={meta.targetLabel ?? "Target"}>
                <input
                  required
                  value={target}
                  onChange={(e) => setTarget(e.target.value)}
                  placeholder={meta.targetPlaceholder}
                  className={inputClass}
                />
              </Field>
            )}

            {meta.needsCommand && (
              <Field label="Command (one argument per line)">
                <textarea
                  required
                  rows={3}
                  value={commandText}
                  onChange={(e) => setCommandText(e.target.value)}
                  placeholder={"/usr/bin/curl\n-f\nhttp://localhost:8080/health"}
                  className={`${inputClass} font-mono`}
                />
                <p className="mt-1 text-xs text-text-muted">
                  Run directly, never through a shell - pipes, {"&&"}, and quoting have no effect.
                </p>
              </Field>
            )}

            {meta.needsThreshold && (
              <Field label="Threshold (% used)">
                <input
                  required
                  type="number"
                  min={1}
                  max={100}
                  step={1}
                  value={threshold}
                  onChange={(e) => setThreshold(Number(e.target.value))}
                  className={inputClass}
                />
              </Field>
            )}

            <div className="grid grid-cols-2 gap-3">
              <Field label="Interval (seconds)">
                <input
                  required
                  type="number"
                  min={5}
                  value={intervalSeconds}
                  onChange={(e) => setIntervalSeconds(Number(e.target.value))}
                  className={inputClass}
                />
              </Field>
              <Field label="Timeout (seconds)">
                <input
                  required
                  type="number"
                  min={1}
                  max={60}
                  value={timeoutSeconds}
                  onChange={(e) => setTimeoutSeconds(Number(e.target.value))}
                  className={inputClass}
                />
              </Field>
              <Field label="Max retries">
                <input
                  type="number"
                  min={0}
                  max={5}
                  value={maxRetries}
                  onChange={(e) => setMaxRetries(Number(e.target.value))}
                  className={inputClass}
                />
              </Field>
              <Field label="Retry delay (seconds)">
                <input
                  type="number"
                  min={0}
                  max={60}
                  value={retryDelaySeconds}
                  onChange={(e) => setRetryDelaySeconds(Number(e.target.value))}
                  className={inputClass}
                />
              </Field>
            </div>

            {error && <p className="text-xs text-status-unhealthy">{error}</p>}
          </div>
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-5 py-4">
          <Button type="button" variant="ghost" onClick={onCancel} disabled={submitting}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {submitting ? "Saving..." : existing ? "Save changes" : "Create check"}
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
