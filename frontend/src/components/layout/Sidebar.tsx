import { NAV_ITEMS, type ViewId } from "@/types/nav";
import { NavIcon } from "@/components/ui/NavIcon";
import { useWindowWidth } from "@/hooks/useWindowWidth";

interface SidebarProps {
  active: ViewId;
  onSelect: (view: ViewId) => void;
}

// Three density modes keyed off the window width, per the narrow / medium /
// wide layout: icons only, labels only, then both once there is room. The
// thresholds sit well clear of the 360px minimum window width the desktop
// shell enforces, so a maximized window always lands in the "both" tier.
type Mode = "icons" | "labels" | "both";

function modeForWidth(width: number): Mode {
  if (width < 520) return "icons";
  if (width < 880) return "labels";
  return "both";
}

export function Sidebar({ active, onSelect }: SidebarProps) {
  const mode = modeForWidth(useWindowWidth());
  const showIcon = mode !== "labels";
  const showLabel = mode !== "icons";

  const widthClass = mode === "icons" ? "w-14" : mode === "labels" ? "w-40" : "w-56";

  return (
    <nav
      className={`flex ${widthClass} shrink-0 flex-col border-r border-border bg-surface-1 py-3 transition-[width]`}
    >
      <div
        className={`flex items-center gap-2 pb-3 ${
          mode === "icons" ? "justify-center px-2" : "px-4"
        }`}
      >
        <img src="/logo-small.png" alt="" className="h-5 w-5 shrink-0" />
        {showLabel && (
          <span className="truncate text-sm font-semibold tracking-wide text-text-primary">
            Weloury
          </span>
        )}
      </div>
      <ul className="flex flex-1 flex-col gap-0.5 px-2">
        {NAV_ITEMS.map((item) => {
          const isActive = active === item.id;
          return (
            <li key={item.id}>
              <button
                onClick={() => onSelect(item.id)}
                aria-current={isActive ? "page" : undefined}
                aria-label={item.label}
                title={showLabel ? undefined : item.label}
                className={`flex w-full items-center rounded-md text-sm transition-colors ${
                  mode === "icons" ? "justify-center px-0 py-2" : "gap-2.5 px-2.5 py-1.5"
                } ${
                  isActive
                    ? "bg-surface-2 text-text-primary"
                    : "text-text-secondary hover:bg-surface-2 hover:text-text-primary"
                }`}
              >
                {showIcon && (
                  <span className="shrink-0">
                    <NavIcon id={item.id} className="h-[18px] w-[18px]" />
                  </span>
                )}
                {showLabel && <span className="truncate">{item.label}</span>}
                {showLabel && item.phase > 1 && (
                  <span className="ml-auto text-xs text-text-muted">P{item.phase}</span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
