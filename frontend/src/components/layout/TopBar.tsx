import type { ConnectionState } from "@/hooks/useBackendConnection";
import type { ThemeSelection } from "@/hooks/useTheme";
import { Button } from "@/components/ui/Button";

interface TopBarProps {
  connection: ConnectionState;
  theme: ThemeSelection["mode"];
  onToggleTheme: () => void;
  onOpenCommandPalette: () => void;
}

const CONNECTION_LABEL: Record<ConnectionState["status"], string> = {
  connecting: "Connecting to Weloury core...",
  connected: "Connected",
  error: "Disconnected",
};

const CONNECTION_DOT: Record<ConnectionState["status"], string> = {
  connecting: "bg-status-degraded",
  connected: "bg-status-healthy",
  error: "bg-status-unhealthy",
};

export function TopBar({ connection, theme, onToggleTheme, onOpenCommandPalette }: TopBarProps) {
  return (
    <header className="flex h-12 shrink-0 items-center justify-between border-b border-border bg-surface-1 px-4">
      <button
        onClick={onOpenCommandPalette}
        className="flex items-center gap-2 rounded-md border border-border px-2.5 py-1 text-xs text-text-muted hover:border-border-strong hover:text-text-secondary"
      >
        <span>Search or jump to...</span>
        <kbd className="rounded border border-border-strong bg-surface-2 px-1 font-mono text-[10px]">⌘K</kbd>
      </button>

      <div className="flex items-center gap-4">
        <span className="flex items-center gap-1.5 text-xs text-text-secondary">
          <span className={`h-1.5 w-1.5 rounded-full ${CONNECTION_DOT[connection.status]}`} />
          {CONNECTION_LABEL[connection.status]}
        </span>
        <Button variant="ghost" onClick={onToggleTheme} aria-label="Toggle theme">
          {theme === "dark" ? "Light mode" : "Dark mode"}
        </Button>
      </div>
    </header>
  );
}
