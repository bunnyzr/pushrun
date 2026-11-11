import { useTranslation } from "react-i18next";
import RelativeTime from "@/components/RelativeTime";
import type { InstanceState } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useMemo, useRef } from "react";

export type InstanceFilter = "all" | "running" | "stopped" | "failed";

interface InstanceListProps {
  instances: InstanceState[];
  /** Unfiltered count, used for the chip labels. */
  all: InstanceState[];
  filter: InstanceFilter;
  onFilterChange: (filter: InstanceFilter) => void;
  selectedId: string | null;
  onSelect: (id: string) => void;
}

function statusTone(status: string): string {
  if (status === "RUNNING") return "bg-success";
  if (status === "FAILED") return "bg-destructive";
  return "bg-muted-foreground/40";
}

export default function InstanceList({
  instances,
  all,
  filter,
  onFilterChange,
  selectedId,
  onSelect,
}: InstanceListProps) {
  const { t } = useTranslation();
  const listRef = useRef<HTMLDivElement>(null);

  const onListKeyDown = (event: React.KeyboardEvent) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    const items = Array.from(
      listRef.current?.querySelectorAll<HTMLButtonElement>("[data-list-item]") ?? [],
    );
    if (items.length === 0) return;
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    const next =
      event.key === "ArrowDown"
        ? Math.min(current + 1, items.length - 1)
        : Math.max(current - 1, 0);
    event.preventDefault();
    items[next]?.focus();
  };

  const counts = useMemo(() => {
    const next: Record<InstanceFilter, number> = {
      all: all.length,
      running: 0,
      stopped: 0,
      failed: 0,
    };
    for (const instance of all) {
      if (instance.status === "RUNNING") next.running++;
      else if (instance.status === "FAILED") next.failed++;
      else next.stopped++;
    }
    return next;
  }, [all]);

  const filters: { value: InstanceFilter; label: string }[] = [
    { value: "all", label: t("instances.filters.all") },
    { value: "running", label: t("instances.filters.running") },
    { value: "stopped", label: t("instances.filters.stopped") },
    { value: "failed", label: t("instances.filters.failed") },
  ];

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-1.5 px-1">
        {filters.map(({ value, label }) => (
          <button
            key={value}
            type="button"
            aria-pressed={filter === value}
            onClick={() => onFilterChange(value)}
            className={cn(
              "rounded-md border px-2.5 py-1 text-[11.5px] font-medium transition-colors",
              filter === value
                ? "border-primary bg-accent text-foreground"
                : "border-border bg-card text-muted-foreground hover:border-line-strong hover:text-foreground",
            )}
          >
            {label} {counts[value]}
          </button>
        ))}
      </div>

      {instances.length === 0 ? (
        <p className="px-2 py-3 text-sm text-muted-foreground">{t("instances.emptyFilter")}</p>
      ) : (
        <div ref={listRef} className="space-y-0.5" onKeyDown={onListKeyDown}>
          {instances.map((inst) => {
            const id = `${inst.project}/${inst.instance}`;
            const selected = selectedId === id;
            const meta: string[] = [t(`status.${inst.status.toLowerCase()}`)];
            if (inst.branch) meta.push(inst.branch);
            if (inst.commit) meta.push(inst.commit.slice(0, 7));
            if (typeof inst.port === "number" && inst.port > 0) meta.push(`:${inst.port}`);

            return (
              <button
                key={id}
                type="button"
                data-list-item
                onClick={() => onSelect(id)}
                className={cn(
                  "grid w-full grid-cols-[8px_minmax(0,1fr)] items-start gap-2.5 rounded-md border-l-2 border-transparent px-3 py-2.5 text-left transition-colors hover:bg-secondary",
                  selected && "border-ring bg-accent",
                )}
              >
                <span
                  aria-hidden="true"
                  className={cn("mt-1.5 h-2 w-2 rounded-full", statusTone(inst.status))}
                />
                <span className="min-w-0">
                  <span className="flex items-baseline justify-between gap-2">
                    <span className="truncate font-mono text-[13.5px] font-semibold">{id}</span>
                    <RelativeTime
                      ts={inst.updated_at}
                      className="flex-none font-mono text-[10.5px] text-muted-foreground"
                    />
                  </span>
                  <span className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 font-mono text-[11px] text-muted-foreground">
                    {meta.map((part, index) => (
                      <span key={`${part}-${index}`} className="inline-flex items-center gap-1.5">
                        {index > 0 && <span aria-hidden="true">·</span>}
                        <span>{part}</span>
                      </span>
                    ))}
                  </span>
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
