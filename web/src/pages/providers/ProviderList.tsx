import type { Provider } from "@/lib/types";
import { cn } from "@/lib/utils";
import LifecycleBadge from "./LifecycleBadge";
import { lifecycleStatus } from "./lifecycle";

interface ProviderListProps {
  providers: Provider[];
  selectedId: string | null;
  onSelect: (id: string) => void;
}

export default function ProviderList({ providers, selectedId, onSelect }: ProviderListProps) {
  return (
    <div className="divide-y divide-border/70">
      {providers.map((provider) => {
        const selected = selectedId === provider.id;
        return (
          <button
            key={provider.id}
            type="button"
            onClick={() => onSelect(provider.id)}
            className={cn(
              "w-full px-3 py-2.5 text-left transition-colors hover:bg-secondary",
              selected && "bg-accent",
            )}
          >
            <div className="flex items-center justify-between gap-2">
              <span className={cn("truncate font-semibold", selected && "text-accent-foreground")}>
                {provider.name}
              </span>
              <LifecycleBadge status={lifecycleStatus(provider)} />
            </div>
            <div className="mt-1 truncate font-mono text-xs text-muted-foreground">
              {provider.id}
            </div>
          </button>
        );
      })}
    </div>
  );
}
