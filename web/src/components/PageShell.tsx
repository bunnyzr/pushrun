import { PanelLeftClose, PanelLeftOpen, RefreshCw, Search } from "lucide-react";
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
}

const MIN_WIDTH = 220;
const MAX_WIDTH = 480;
const DEFAULT_WIDTH = 300;

function readNumber(key: string): number | null {
  try {
    const raw = localStorage.getItem(key);
    const n = raw === null ? NaN : Number(raw);
    return Number.isFinite(n) ? n : null;
  } catch {
    return null;
  }
}

// PageShell is the shared two-column console layout: a sidebar (title,
// count, refresh, search, list) that can be resized by dragging its divider
// and collapsed to a slim rail, next to a scrollable detail area. Width and
// collapsed state persist per page id.
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
}: PageShellProps) {
  const { t } = useTranslation();
  const widthKey = `pushrun:shell:${id}:width`;
  const [width, setWidth] = useState(() => readNumber(widthKey) ?? DEFAULT_WIDTH);
  const [collapsed, setCollapsed] = useState(false);
  const drag = useRef<{ startX: number; startWidth: number } | null>(null);

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

  const toggle = (
    <button
      type="button"
      onClick={() => setCollapsed((v) => !v)}
      title={collapsed ? t("shell.expand") : t("shell.collapse")}
      aria-label={collapsed ? t("shell.expand") : t("shell.collapse")}
      className="inline-flex items-center rounded-md p-1.5 text-muted-foreground hover:bg-secondary hover:text-foreground"
    >
      {collapsed ? (
        <PanelLeftOpen size={15} aria-hidden="true" />
      ) : (
        <PanelLeftClose size={15} aria-hidden="true" />
      )}
    </button>
  );

  if (collapsed) {
    return (
      <div className="flex min-h-0 flex-1">
        <aside className="flex w-11 flex-none flex-col items-center border-r bg-card py-3">
          {toggle}
        </aside>
        <section className="min-w-0 flex-1 overflow-y-auto">
          <div className="mx-auto max-w-[1400px] p-5">{children}</div>
        </section>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1">
      <aside
        style={{ width }}
        className="flex flex-none flex-col overflow-hidden border-r bg-card"
      >
        <div className="flex items-center gap-2 border-b px-4 py-3">
          <h1 className="text-[15px] font-bold">{title}</h1>
          {typeof count === "number" && (
            <span className="rounded-full bg-secondary px-2 py-0.5 text-xs font-semibold text-muted-foreground">
              {count}
            </span>
          )}
          <div className="ml-auto flex items-center gap-1">
            {onRefresh && (
              <button
                type="button"
                aria-label={t("common.refresh")}
                onClick={onRefresh}
                className="inline-flex items-center rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-accent-foreground"
              >
                <RefreshCw
                  size={13}
                  aria-hidden="true"
                  className={cn(refreshing && "animate-spin")}
                />
              </button>
            )}
            {toggle}
          </div>
        </div>
        {onSearchChange && (
          <div className="border-b px-3 py-2">
            <div className="flex items-center gap-2 rounded-md border bg-background px-2.5">
              <Search size={13} className="flex-none text-muted-foreground" aria-hidden="true" />
              <input
                value={searchValue ?? ""}
                onChange={(e) => onSearchChange(e.target.value)}
                placeholder={searchPlaceholder}
                className="h-8 w-full bg-transparent text-[13px] outline-none placeholder:text-muted-foreground"
              />
            </div>
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto p-2">{sidebar}</div>
      </aside>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t("shell.resize")}
        title={t("shell.resize")}
        onPointerDown={onDividerDown}
        onPointerMove={onDividerMove}
        onPointerUp={onDividerUp}
        className="w-1 flex-none cursor-col-resize bg-transparent transition-colors hover:bg-primary/40"
      />
      <section className="min-w-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[1400px] p-5">{children}</div>
      </section>
    </div>
  );
}
