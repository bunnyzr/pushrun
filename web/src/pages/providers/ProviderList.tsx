import { useRef } from "react";
import { useTranslation } from "react-i18next";
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

  return (
    <div ref={listRef} className="space-y-0.5" onKeyDown={onListKeyDown}>
      {providers.map((provider) => {
        const selected = selectedId === provider.id;
        const cached = (provider.warmups ?? []).filter((entry) => !entry.warming).length;
        return (
          <button
            key={provider.id}
            type="button"
            data-list-item
            onClick={() => onSelect(provider.id)}
            className={cn(
              "w-full rounded-md border-l-2 border-transparent px-3 py-2.5 text-left transition-colors hover:bg-secondary",
              selected && "border-ring bg-accent",
            )}
          >
            <div className="flex items-start justify-between gap-2">
              <span className="truncate text-[14px] font-semibold">{provider.name}</span>
              <span className="flex-none">
                <LifecycleBadge status={lifecycleStatus(provider)} />
              </span>
            </div>
            <div className="mt-0.5 flex items-center gap-2 font-mono text-[11px] text-muted-foreground">
              <span className="truncate">{provider.id}</span>
              {cached > 0 && (
                <span className="ml-auto flex-none text-[10.5px]">
                  {t("providers.card.cacheCount", { count: cached })}
                </span>
              )}
            </div>
          </button>
        );
      })}
    </div>
  );
}
