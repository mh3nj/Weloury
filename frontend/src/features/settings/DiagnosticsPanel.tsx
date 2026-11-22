import { useState, type ReactNode } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import type { DiagnosticsReport } from "@/types/diagnostics";

/**
 * Diagnostics area (spec section 23): an on-demand snapshot pulled from
 * subsystems that already exist (system info, disk usage, network
 * config/listening ports, non-running services, failing health checks,
 * recent warning/critical events) - see backend/internal/diagnostics'
 * package doc comment. Deliberately not its own top-level nav item: the
 * suggested navigation list (spec section 20) doesn't include one, so
 * this lives as a section of Settings instead.
 *
 * Export writes exactly what diagnostics.run returned, formatted as
 * JSON - no secrets pass through this at all (see
 * backend/internal/diagnostics' RecentIssues doc comment and
 * SECURITY.md's "Safe logging"), so there's nothing to sanitize
 * client-side beyond what the backend already never included.
 */
export function DiagnosticsPanel({ client }: { client: FwpClient }) {
  const [report, setReport] = useState<DiagnosticsReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run() {
    setLoading(true);
    setError(null);
    try {
      const result = await client.request<DiagnosticsReport>("diagnostics.run");
      setReport(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }

  function exportReport() {
    if (!report) return;
    const text = JSON.stringify(report, null, 2);
    const blob = new Blob([text], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `weloury-diagnostics-${new Date().toISOString().replace(/[:.]/g, "-")}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <Panel>
      <PanelHeader>
        <h2 className="text-sm font-semibold">Diagnostics</h2>
        <div className="flex gap-2">
          <Button variant="secondary" onClick={run} disabled={loading}>
            {loading ? "Running..." : "Run diagnostics"}
          </Button>
          <Button variant="ghost" onClick={exportReport} disabled={!report}>
            Export
          </Button>
        </div>
      </PanelHeader>

      <div className="p-4 text-sm">
        {error && <p className="text-status-unhealthy">{error}</p>}
        {!report && !error && (
          <p className="text-xs text-text-muted">
            Runs a snapshot check across system info, disks, network, services, and health checks -
            useful to attach when reporting a problem.
          </p>
        )}
        {report && (
          <div className="flex flex-col gap-4">
            <p className="text-xs text-text-muted">Generated {new Date(report.generated_at).toLocaleString()}</p>

            <Section title="System" error={report.system.error}>
              {report.system.info && (
                <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                  <dt className="text-text-muted">Hostname</dt>
                  <dd>{report.system.info.hostname}</dd>
                  <dt className="text-text-muted">OS</dt>
                  <dd>
                    {report.system.info.os} {report.system.info.os_version}
                  </dd>
                </dl>
              )}
            </Section>

            <Section title="Storage" error={report.storage.error}>
              <p className="text-xs text-text-secondary">{report.storage.disks?.length ?? 0} disk(s) found</p>
            </Section>

            <Section title="Network" error={report.network.error}>
              <p className="text-xs text-text-secondary">
                {report.network.interfaces?.length ?? 0} interface(s),{" "}
                {report.network.listening_ports?.length ?? 0} listening port(s)
              </p>
            </Section>

            <Section title="Services" error={report.services.error}>
              {report.services.not_running && report.services.not_running.length > 0 ? (
                <ul className="list-disc pl-4 text-xs text-text-secondary">
                  {report.services.not_running.map((s) => (
                    <li key={s.name}>
                      {s.display_name || s.name} - {s.state}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-xs text-text-secondary">All discovered services are running.</p>
              )}
            </Section>

            <Section title="Health checks">
              {report.health.failing && report.health.failing.length > 0 ? (
                <ul className="list-disc pl-4 text-xs text-text-secondary">
                  {report.health.failing.map((c) => (
                    <li key={c.id}>
                      {c.name} - {c.state}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-xs text-text-secondary">No failing health checks.</p>
              )}
            </Section>

            <Section title="Recent issues">
              {report.recent_issues.length > 0 ? (
                <ul className="space-y-1 text-xs text-text-secondary">
                  {report.recent_issues.map((e) => (
                    <li key={e.id} className="font-mono">
                      {new Date(e.timestamp).toLocaleTimeString()} [{e.severity}] {e.type}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-xs text-text-secondary">No recent warnings or errors.</p>
              )}
            </Section>
          </div>
        )}
      </div>
    </Panel>
  );
}

function Section({ title, error, children }: { title: string; error?: string; children: ReactNode }) {
  return (
    <div className="border-t border-border pt-3">
      <h3 className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-text-muted">{title}</h3>
      {error ? <p className="text-xs text-status-degraded">Unavailable: {error}</p> : children}
    </div>
  );
}
