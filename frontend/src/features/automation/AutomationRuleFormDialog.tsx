import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Button } from "@/components/ui/Button";
import { useDialogKeyboard } from "@/hooks/useDialogKeyboard";
import { ACTION_TYPE_META, ACTION_TYPE_ORDER, COMMON_EVENT_TYPES } from "./actionTypeMeta";
import type {
  Action,
  ActionType,
  AutomationRuleInput,
  AutomationRuleStatus,
  Condition,
  ScheduledTask,
  TriggerType,
} from "@/types/automation";

interface AutomationRuleFormDialogProps {
  client: FwpClient;
  open: boolean;
  existing?: AutomationRuleStatus;
  onCancel: () => void;
  onSubmit: (input: AutomationRuleInput) => Promise<void>;
}

/** An Action, with array fields edited as one-per-line text - same reasoning as HealthCheckFormDialog's command textarea: argv is exec'd directly, never through a shell, so "one per line" avoids implying quoting/pipes would work. */
interface ActionDraft {
  type: ActionType;
  processName: string;
  launchCommandText: string;
  serviceName: string;
  commandArgsText: string;
  message: string;
}

function draftFromAction(a: Action): ActionDraft {
  return {
    type: a.type,
    processName: a.process_name ?? "",
    launchCommandText: (a.launch_command ?? []).join("\n"),
    serviceName: a.service_name ?? "",
    commandArgsText: (a.command_args ?? []).join("\n"),
    message: a.message ?? "",
  };
}

function emptyDraft(): ActionDraft {
  return { type: "notify", processName: "", launchCommandText: "", serviceName: "", commandArgsText: "", message: "" };
}

function draftToAction(d: ActionDraft): Action {
  const meta = ACTION_TYPE_META[d.type];
  const splitLines = (text: string) =>
    text
      .split("\n")
      .map((l) => l.trim())
      .filter((l) => l.length > 0);
  return {
    type: d.type,
    process_name: meta.needsProcessName ? d.processName : undefined,
    launch_command: meta.needsLaunchCommand ? splitLines(d.launchCommandText) : undefined,
    service_name: meta.needsServiceName ? d.serviceName : undefined,
    command_args: meta.needsCommandArgs ? splitLines(d.commandArgsText) : undefined,
    message: meta.needsMessage ? d.message : undefined,
  };
}

export function AutomationRuleFormDialog({ client, open, existing, onCancel, onSubmit }: AutomationRuleFormDialogProps) {
  const [name, setName] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [triggerType, setTriggerType] = useState<TriggerType>("event");
  const [eventType, setEventType] = useState("");
  const [scheduleId, setScheduleId] = useState("");
  const [conditions, setConditions] = useState<Condition[]>([]);
  const [actions, setActions] = useState<ActionDraft[]>([emptyDraft()]);
  const [cooldownSeconds, setCooldownSeconds] = useState(0);
  const [schedules, setSchedules] = useState<ScheduledTask[]>([]);
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
      setEnabled(existing.enabled);
      setTriggerType(existing.trigger.type);
      setEventType(existing.trigger.event_type ?? "");
      setScheduleId(existing.trigger.schedule_id ?? "");
      setConditions(existing.conditions ?? []);
      setActions(existing.actions.length > 0 ? existing.actions.map(draftFromAction) : [emptyDraft()]);
      setCooldownSeconds(existing.cooldown_seconds ?? 0);
    } else {
      setName("");
      setEnabled(true);
      setTriggerType("event");
      setEventType("");
      setScheduleId("");
      setConditions([]);
      setActions([emptyDraft()]);
      setCooldownSeconds(0);
    }

    client
      .request<ScheduledTask[]>("scheduler.list")
      .then((v) => setSchedules(v ?? []))
      .catch(() => setSchedules([]));
  }, [open, existing, client]);

  useDialogKeyboard(open, containerRef, onCancel, nameRef);

  if (!open) return null;

  function updateCondition(i: number, patch: Partial<Condition>) {
    setConditions((prev) => prev.map((c, idx) => (idx === i ? { ...c, ...patch } : c)));
  }

  function updateAction(i: number, patch: Partial<ActionDraft>) {
    setActions((prev) => prev.map((a, idx) => (idx === i ? { ...a, ...patch } : a)));
  }

  function moveAction(i: number, dir: -1 | 1) {
    setActions((prev) => {
      const next = [...prev];
      const j = i + dir;
      if (j < 0 || j >= next.length) return prev;
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit({
        name,
        enabled,
        trigger:
          triggerType === "event"
            ? { type: "event", event_type: eventType }
            : { type: "schedule", schedule_id: scheduleId },
        conditions: conditions.filter((c) => c.field.trim() !== ""),
        actions: actions.map(draftToAction),
        cooldown_seconds: cooldownSeconds,
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
      aria-labelledby="automation-form-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    >
      <form
        onSubmit={handleSubmit}
        className="flex max-h-[85vh] w-full max-w-xl flex-col rounded-lg border border-border-strong bg-surface-2 shadow-xl"
      >
        <div className="border-b border-border px-5 py-4">
          <h2 id="automation-form-title" className="text-sm font-semibold text-text-primary">
            {existing ? "Edit automation rule" : "New automation rule"}
          </h2>
        </div>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          <div className="flex flex-col gap-5 text-sm">
            <Field label="Name">
              <input
                ref={nameRef}
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. restart worker on crash"
                className={inputClass}
              />
            </Field>

            <label className="flex items-center gap-2 text-xs text-text-secondary">
              <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
              Enabled
            </label>

            <Section title="Trigger">
              <Field label="When">
                <select
                  value={triggerType}
                  onChange={(e) => setTriggerType(e.target.value as TriggerType)}
                  className={inputClass}
                >
                  <option value="event">An event happens</option>
                  <option value="schedule">A schedule fires</option>
                </select>
              </Field>
              {triggerType === "event" && (
                <Field label="Event type">
                  <input
                    required
                    list="common-event-types"
                    value={eventType}
                    onChange={(e) => setEventType(e.target.value)}
                    placeholder="e.g. health.check_failed"
                    className={inputClass}
                  />
                  <datalist id="common-event-types">
                    {COMMON_EVENT_TYPES.map((t) => (
                      <option key={t} value={t} />
                    ))}
                  </datalist>
                </Field>
              )}
              {triggerType === "schedule" && (
                <Field label="Schedule">
                  <select required value={scheduleId} onChange={(e) => setScheduleId(e.target.value)} className={inputClass}>
                    <option value="" disabled>
                      Select a scheduled task...
                    </option>
                    {schedules.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                  {schedules.length === 0 && (
                    <p className="mt-1 text-xs text-text-muted">
                      No scheduled tasks yet - create one on the Schedules screen first.
                    </p>
                  )}
                </Field>
              )}
            </Section>

            <Section title="Conditions" hint="Optional. All must match the triggering event's payload.">
              {conditions.map((c, i) => (
                <div key={i} className="flex items-center gap-2">
                  <input
                    value={c.field}
                    onChange={(e) => updateCondition(i, { field: e.target.value })}
                    placeholder="field, e.g. check_id"
                    className={inputClass}
                  />
                  <span className="text-text-muted">=</span>
                  <input
                    value={c.equals}
                    onChange={(e) => updateCondition(i, { equals: e.target.value })}
                    placeholder="value"
                    className={inputClass}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    onClick={() => setConditions((prev) => prev.filter((_, idx) => idx !== i))}
                  >
                    Remove
                  </Button>
                </div>
              ))}
              <Button
                type="button"
                variant="ghost"
                onClick={() => setConditions((prev) => [...prev, { field: "", equals: "" }])}
              >
                + Add condition
              </Button>
            </Section>

            <Section title="Actions" hint="Run in order. A failed action doesn't stop the rest from running.">
              {actions.map((a, i) => (
                <div key={i} className="flex flex-col gap-2 rounded-md border border-border p-3">
                  <div className="flex items-center gap-2">
                    <span className="w-5 text-xs text-text-muted">{i + 1}.</span>
                    <select
                      value={a.type}
                      onChange={(e) => updateAction(i, { type: e.target.value as ActionType })}
                      className={inputClass}
                    >
                      {ACTION_TYPE_ORDER.map((t) => (
                        <option key={t} value={t}>
                          {ACTION_TYPE_META[t].label}
                        </option>
                      ))}
                    </select>
                    <Button type="button" variant="ghost" onClick={() => moveAction(i, -1)} disabled={i === 0}>
                      ↑
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={() => moveAction(i, 1)}
                      disabled={i === actions.length - 1}
                    >
                      ↓
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={() => setActions((prev) => prev.filter((_, idx) => idx !== i))}
                      disabled={actions.length === 1}
                    >
                      Remove
                    </Button>
                  </div>

                  {ACTION_TYPE_META[a.type].needsProcessName && (
                    <input
                      required
                      value={a.processName}
                      onChange={(e) => updateAction(i, { processName: e.target.value })}
                      placeholder="Process name, e.g. my-worker"
                      className={inputClass}
                    />
                  )}
                  {ACTION_TYPE_META[a.type].needsLaunchCommand && (
                    <textarea
                      required
                      rows={2}
                      value={a.launchCommandText}
                      onChange={(e) => updateAction(i, { launchCommandText: e.target.value })}
                      placeholder={"Launch command, one argument per line\n/usr/bin/my-worker\n--flag"}
                      className={`${inputClass} font-mono`}
                    />
                  )}
                  {ACTION_TYPE_META[a.type].needsServiceName && (
                    <input
                      required
                      value={a.serviceName}
                      onChange={(e) => updateAction(i, { serviceName: e.target.value })}
                      placeholder="Service name, e.g. nginx"
                      className={inputClass}
                    />
                  )}
                  {ACTION_TYPE_META[a.type].needsCommandArgs && (
                    <textarea
                      required
                      rows={2}
                      value={a.commandArgsText}
                      onChange={(e) => updateAction(i, { commandArgsText: e.target.value })}
                      placeholder={"Command, one argument per line - never a shell\n/usr/bin/curl\n-f\nhttp://localhost/health"}
                      className={`${inputClass} font-mono`}
                    />
                  )}
                  {ACTION_TYPE_META[a.type].needsMessage && (
                    <input
                      required
                      value={a.message}
                      onChange={(e) => updateAction(i, { message: e.target.value })}
                      placeholder="Message"
                      className={inputClass}
                    />
                  )}
                </div>
              ))}
              <Button type="button" variant="ghost" onClick={() => setActions((prev) => [...prev, emptyDraft()])}>
                + Add action
              </Button>
            </Section>

            <Field label="Cooldown (seconds)">
              <input
                type="number"
                min={0}
                value={cooldownSeconds}
                onChange={(e) => setCooldownSeconds(Number(e.target.value))}
                className={inputClass}
              />
              <p className="mt-1 text-xs text-text-muted">
                Minimum time between runs, even if the trigger fires again sooner. 0 means no limit.
              </p>
            </Field>

            {error && <p className="text-xs text-status-unhealthy">{error}</p>}
          </div>
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-5 py-4">
          <Button type="button" variant="ghost" onClick={onCancel} disabled={submitting}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {submitting ? "Saving..." : existing ? "Save changes" : "Create rule"}
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

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 border-t border-border pt-4">
      <div>
        <h3 className="text-xs font-semibold uppercase tracking-wide text-text-muted">{title}</h3>
        {hint && <p className="text-xs text-text-muted">{hint}</p>}
      </div>
      {children}
    </div>
  );
}
