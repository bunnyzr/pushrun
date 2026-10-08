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
    <div className="space-y-2">
      {instances.map((inst) => {
        const id = `${inst.project}/${inst.instance}`;
        return (
          <button
            key={id}
            type="button"
            onClick={() => onSelect(id)}
            className={cn(
              "w-full rounded-lg border p-3 text-left transition-colors hover:bg-accent/40",
              selectedId === id && "border-primary bg-accent/50",
            )}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium">{id}</span>
              <StatusBadge status={inst.status} />
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-muted-foreground">
              {inst.branch && <span>{inst.branch}</span>}
              {inst.commit && <span className="font-mono">{inst.commit.slice(0, 7)}</span>}
              {typeof inst.port === "number" && inst.port > 0 && <span>:{inst.port}</span>}
            </div>
            {inst.url && (
              <a
                href={inst.url}
                target="_blank"
                rel="noreferrer"
                onClick={(e) => e.stopPropagation()}
                className="mt-1 block truncate text-xs text-primary underline"
              >
                {inst.url}
              </a>
            )}
          </button>
        );
      })}
    </div>
  );
}
