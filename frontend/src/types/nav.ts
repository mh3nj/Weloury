export type ViewId =
  | "overview"
  | "processes"
  | "services"
  | "containers"
  | "network"
  | "gpu"
  | "storage"
  | "startup"
  | "logs"
  | "health"
  | "events"
  | "automation"
  | "schedules"
  | "settings";

export interface NavItem {
  id: ViewId;
  label: string;
  /** Which build phase implements this view; used to render "arrives in Phase N" placeholders. */
  phase: number;
}

export const NAV_ITEMS: NavItem[] = [
  { id: "overview", label: "Overview", phase: 1 },
  { id: "processes", label: "Processes", phase: 2 },
  { id: "services", label: "Services", phase: 3 },
  { id: "containers", label: "Containers", phase: 6 },
  { id: "network", label: "Network", phase: 6 },
  { id: "gpu", label: "GPU", phase: 6 },
  { id: "storage", label: "Storage", phase: 2 },
  { id: "startup", label: "Startup", phase: 6 },
  { id: "logs", label: "Logs", phase: 4 },
  { id: "health", label: "Health", phase: 4 },
  { id: "events", label: "Events", phase: 4 },
  { id: "automation", label: "Automation", phase: 5 },
  { id: "schedules", label: "Schedules", phase: 5 },
  { id: "settings", label: "Settings", phase: 1 },
];

/**
 * The highest build phase whose UI is actually implemented. NavItems with
 * a phase beyond this show PlaceholderPage instead of their real
 * component - see App.tsx. Bump this alongside adding a phase's pages,
 * not before.
 */
export const IMPLEMENTED_PHASE = 6;
