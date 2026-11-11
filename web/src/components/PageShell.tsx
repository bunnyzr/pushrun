import { ChevronLeft, PanelLeftClose, PanelLeftOpen, RefreshCw, Search } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";

interface PageShellProps {
  /** Stable id for persisting the sidebar width across sessions. */
  id: string;
  title: string;
  count?: number;
  onRefresh?: () => void;
  refreshing?: boolean;
  searchValue?: string;
  onSearchChange?: (value: string) => void;
  searchPlaceholder?: string;
  sidebar: ReactNode;
  children: ReactNode;
  /** On narrow screens the list and detail are separate screens. */
  detailActive?: boolean;
  onBack?: () => void;
}

const MIN_WIDTH = 240;
const MAX_WIDTH = 440;
const DEFAULT_WIDTH = 316;

function readNumber(key: string): number | null {
  try {
    const raw = localStorage.getItem(key);
    const n = raw === null ? NaN : Number(raw);
    return Number.isFinite(n) ? n : null;
  } catch {
    return null;
  }
}

function readBool(key: string): boolean {
  try {
    return localStorage.getItem(key) === "true";
  } catch {
    return false;
  }
}

// PageShell is the shared console layout: a resizable context list next to a
// scrollable detail area. On narrow screens it becomes a list -> detail flow.
export default function PageShell({
  id,
  title,
  count,
  onRefresh,
  refreshing,
  searchValue,
  onSearchChange,
  searchPlaceholder,
  sidebar,
  children,
  detailActive = false,
  onBack,
}: PageShellProps) {
  const { t } = useTranslation();
  const widthKey = `pushrun:shell:${id}:width`;
  const collapsedKey = `pushrun:shell:${id}:collapsed`;
  const [width, setWidth] = useState(() => readNumber(widthKey) ?? DEFAULT_WIDTH);
  const [collapsed, setCollapsed] = useState(() => readBool(collapsedKey));
  const drag = useRef<{ startX: number; startWidth: number } | null>(null);

  const toggleCollapsed = () => {
    setCollapsed((value) => {
      try {
        localStorage.setItem(collapsedKey, String(!value));
      } catch {
        // Storage unavailable: collapse still applies for this session.
      }
      return !value;
    });
  };

  const onDividerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    drag.current = { startX: e.clientX, startWidth: width };
    e.currentTarget.setPointerCapture(e.pointerId);
  };
  const onDividerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return;
    const next = Math.min(
      MAX_WIDTH,
      Math.max(MIN_WIDTH, drag.current.startWidth + e.clientX - drag.current.startX),
    );
    setWidth(next);
    try {
      localStorage.setItem(widthKey, String(next));
    } catch {
      // Storage unavailable (private mode): width just doesn't persist.
    }
  };
  const onDividerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    drag.current = null;
    e.currentTarget.releasePointerCapture(e.pointerId);
  };

  return (
    <div
      className={cn(
        "console-shell flex min-h-0 flex-1",
        detailActive && "detail-active",
        collapsed && "sidebar-collapsed",
      )}
      data-page={id}
    >
      <aside
        style={{ width }}
        className="console-sidebar flex flex-none flex-col overflow-hidden border-r bg-secondary/60"
      >
        <div className="flex items-start gap-2 border-b px-4 py-3.5">
          <div className="min-w-0">
            <div className="eyebrow">{t("shell.workspace")}</div>
            <div className="mt-1 flex items-center gap-2">
              <h1 className="truncate text-[18px] font-semibold tracking-[-0.015em]">{title}</h1>
              {typeof count === "number" && (
                <span className="rounded-full border bg-card px-2 py-0.5 font-mono text-[11px] font-medium text-muted-foreground">
                  {count}
                </span>
              )}
            </div>
          </div>
          <div className="ml-auto flex flex-none items-center gap-1">
            {onRefresh && (
              <button
                type="button"
                aria-label={t("common.refresh")}
                title={t("common.refresh")}
                onClick={onRefresh}
                className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground max-sm:h-10 max-sm:w-10"
              >
                <RefreshCw
                  size={14}
                  aria-hidden="true"
                  className={cn(refreshing && "animate-spin")}
                />
              </button>
            )}
            <button
              type="button"
              aria-label={t("shell.collapseList")}
              title={t("shell.collapseList")}
              onClick={toggleCollapsed}
              className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground max-sm:hidden"
            >
              <PanelLeftClose size={15} aria-hidden="true" />
            </button>
          </div>
        </div>
        {onSearchChange && (
          <div className="border-b px-3 py-2.5">
            <div className="flex h-9 items-center gap-2 rounded-md border bg-card px-2.5">
              <Search size={14} className="flex-none text-muted-foreground" aria-hidden="true" />
              <input
                value={searchValue ?? ""}
                onChange={(e) => onSearchChange(e.target.value)}
                placeholder={searchPlaceholder}
                className="h-full w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
              />
            </div>
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto p-2 pb-20 md:pb-2">{sidebar}</div>
      </aside>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t("shell.resize")}
        title={t("shell.resize")}
        onPointerDown={onDividerDown}
        onPointerMove={onDividerMove}
        onPointerUp={onDividerUp}
        className="console-divider w-1 flex-none cursor-col-resize bg-transparent transition-colors hover:bg-ring/30"
      />
      <section className="console-content min-w-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[1240px] p-4 pb-20 sm:p-6 sm:pb-20 md:pb-6 lg:px-[34px] lg:py-[26px]">
          {collapsed && (
            <button
              type="button"
              aria-label={t("shell.expandList")}
              title={t("shell.expandList")}
              onClick={toggleCollapsed}
              className="mb-3 hidden h-8 items-center gap-1.5 rounded-md border bg-card px-2.5 text-xs font-medium text-muted-foreground hover:bg-secondary hover:text-foreground md:inline-flex"
            >
              <PanelLeftOpen size={14} aria-hidden="true" />
              {t("shell.expandList")}
            </button>
          )}
          {detailActive && onBack && (
            <button
              type="button"
              onClick={onBack}
              className="console-back mb-4 inline-flex min-h-10 items-center gap-1.5 rounded-md border bg-card px-3 py-2 text-xs font-medium text-muted-foreground hover:text-foreground md:hidden"
            >
              <ChevronLeft size={14} aria-hidden="true" />
              {t("shell.back")}
            </button>
          )}
          {children}
        </div>
      </section>
    </div>
  );
}
