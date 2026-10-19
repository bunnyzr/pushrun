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
    <div className="space-y-1">
      {providers.map((provider) => {
        const selected = selectedId === provider.id;
        return (
          <button
            key={provider.id}
            type="button"
            onClick={() => onSelect(provider.id)}
            className={cn(
              "w-full rounded-md border bg-card px-3 py-2.5 text-left transition-colors hover:bg-secondary",
              selected && "border-primary bg-accent",
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
