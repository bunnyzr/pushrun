import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Download, Plus, Upload } from "lucide-react";
import { Menu } from "@/components/ui/menu";
import { Button } from "@/components/ui/button";
import type { Project } from "@/lib/types";
import { cn } from "@/lib/utils";
import { primarySummary } from "./primarySummary";

export interface ProjectListProps {
  projects: Project[];
  selectedName: string | null;
  importing: boolean;
  bundleImporting: boolean;
  statusByName: Record<string, string | undefined>;
  onSelect: (name: string) => void;
  onCreate: () => void;
  onImport: () => void;
  onImportBundle: () => void;
  onExportBundle: () => void;
}

type Filter = "all" | "running" | "failed";

// Only running/failed projects get a dot. An idle project's "stopped" state
// is the norm and a gray dot on every row is just noise.
function statusTone(status: string | undefined): string | null {
  if (status === "RUNNING") return "bg-success";
  if (status === "FAILED") return "bg-destructive";
  return null;
}

export default function ProjectList({
  projects,
  selectedName,
  importing,
  bundleImporting,
  statusByName,
  onSelect,
  onCreate,
  onImport,
  onImportBundle,
  onExportBundle,
}: ProjectListProps) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState<Filter>("all");
  const listRef = useRef<HTMLDivElement>(null);

  // Arrow keys walk the list; Enter opens the focused row natively.
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

  const visible = projects.filter((project) => {
    if (filter === "all") return true;
    const status = statusByName[project.name];
    return filter === "running" ? status === "RUNNING" : status === "FAILED";
  });
  const running = projects.filter((p) => statusByName[p.name] === "RUNNING").length;
  const failed = projects.filter((p) => statusByName[p.name] === "FAILED").length;

  return (
    <div className="space-y-3">
      <div className="space-y-2 px-1">
        <Button size="sm" className="w-full" onClick={onCreate}>
          <Plus className="h-4 w-4" aria-hidden="true" />
          {t("projects.new")}
        </Button>
        <div className="flex gap-2">
          <Button
            size="sm"
            variant="outline"
            className="flex-1"
            disabled={importing}
            onClick={onImport}
          >
            <Upload className="h-4 w-4" aria-hidden="true" />
            {t("projects.import")}
          </Button>
          <Menu
            label={t("projects.bundleActions")}
            items={[
              {
                key: "import-all",
                label: t("projects.bundleImport"),
                icon: <Upload size={14} aria-hidden="true" />,
                disabled: bundleImporting,
                onSelect: onImportBundle,
              },
              {
                key: "export-all",
                label: t("projects.bundleExport"),
                icon: <Download size={14} aria-hidden="true" />,
                onSelect: onExportBundle,
              },
            ]}
          />
        </div>
      </div>

      <div className="flex items-center gap-1.5 px-1">
        {(
          [
            ["all", t("projects.filters.all"), projects.length],
            ["running", t("projects.filters.running"), running],
            ["failed", t("projects.filters.failed"), failed],
          ] as const
        ).map(([value, label, count]) => (
          <button
            key={value}
            type="button"
            onClick={() => setFilter(value)}
            className={cn(
              "rounded-md border px-2.5 py-1 text-[11.5px] font-medium transition-colors",
              filter === value
                ? "border-primary bg-accent text-foreground"
                : "border-border bg-card text-muted-foreground hover:border-line-strong hover:text-foreground",
            )}
          >
            {label} {count}
          </button>
        ))}
      </div>

      {visible.length === 0 ? (
        <p className="px-2 py-3 text-sm text-muted-foreground">
          {projects.length === 0 ? t("projects.empty") : t("projects.emptyFilter")}
        </p>
      ) : (
        <div ref={listRef} className="space-y-0.5" onKeyDown={onListKeyDown}>
          {visible.map((project) => {
            const summary = primarySummary(project);
            const selected = selectedName === project.name;
            const status = statusByName[project.name];
            const tone = statusTone(status);
            const statusLabel = status ? t(`status.${status.toLowerCase()}`, { defaultValue: status }) : "";
            return (
              <button
                key={project.name}
                type="button"
                data-list-item
                onClick={() => onSelect(project.name)}
                className={cn(
                  "grid w-full grid-cols-[8px_minmax(0,1fr)] items-start gap-2.5 rounded-md border-l-2 border-transparent px-3 py-2.5 text-left transition-colors hover:bg-secondary",
                  selected && "border-ring bg-accent",
                )}
              >
                <span
                  role={tone ? "img" : undefined}
                  aria-label={tone ? statusLabel : undefined}
                  aria-hidden={tone ? undefined : true}
                  title={tone ? statusLabel : undefined}
                  className={cn("mt-1.5 h-2 w-2 rounded-full", tone)}
                />
                <span className="min-w-0">
                  <span className="block truncate text-[14px] font-semibold">
                    {project.display_name || project.name}
                  </span>
                  {summary && (
                    <span className="mt-0.5 block truncate font-mono text-[11.5px] text-muted-foreground">
                      {project.display_name ? `${project.name} · ${summary}` : summary}
                    </span>
                  )}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
