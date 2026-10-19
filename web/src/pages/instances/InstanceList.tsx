import RelativeTime from "@/components/RelativeTime";
import StatusBadge from "@/components/StatusBadge";
import type { InstanceState } from "@/lib/types";
import { cn } from "@/lib/utils";

interface InstanceListProps {
  instances: InstanceState[];
  selectedId: string | null;
  onSelect: (id: string) => void;
}

export default function InstanceList({ instances, selectedId, onSelect }: InstanceListProps) {
  return (
    <div className="space-y-1">
      {instances.map((inst) => {
        const id = `${inst.project}/${inst.instance}`;
        const selected = selectedId === id;
        return (
          <button
            key={id}
            type="button"
            onClick={() => onSelect(id)}
            className={cn(
              "w-full rounded-md border bg-card px-3 py-2.5 text-left transition-colors hover:bg-secondary",
              selected && "border-primary bg-accent",
            )}
          >
            <div className="flex items-center justify-between gap-2">
              <span className={cn("truncate font-semibold", selected && "text-accent-foreground")}>
                {id}
              </span>
              <StatusBadge status={inst.status} />
            </div>
            <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
              {inst.branch && <span className="truncate">{inst.branch}</span>}
              {inst.commit && <span className="font-mono">{inst.commit.slice(0, 7)}</span>}
              {typeof inst.port === "number" && inst.port > 0 && <span>:{inst.port}</span>}
              <RelativeTime ts={inst.updated_at} className="ml-auto flex-none text-xs" />
            </div>
          </button>
        );
      })}
    </div>
  );
}
