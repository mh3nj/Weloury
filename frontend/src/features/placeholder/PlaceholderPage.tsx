import type { NavItem } from "@/types/nav";
import { Panel } from "@/components/ui/Panel";

export function PlaceholderPage({ item }: { item: NavItem }) {
  return (
    <div className="flex h-full items-center justify-center">
      <Panel className="max-w-sm px-6 py-8 text-center">
        <p className="text-sm font-medium text-text-primary">{item.label}</p>
        <p className="mt-1.5 text-sm text-text-secondary">Arrives in Phase {item.phase} of the build plan.</p>
      </Panel>
    </div>
  );
}
