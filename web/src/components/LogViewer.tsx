import { ChevronDown, ChevronUp, Copy, Download, Search, TextWrap, X } from "lucide-react";
import { useDeferredValue, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import StatusBadge from "@/components/StatusBadge";
import { toast } from "@/lib/toast";
import { cn } from "@/lib/utils";

interface LogViewerProps {
  lines: string[];
  /** Terminal status of the stream, shown in the toolbar once known. */
  status?: string | null;
  emptyText?: string;
  downloadName?: string;
  /** When set, the download button fetches the complete server-side file. */
  onDownload?: () => void;
  /** Shown above the lines when the console buffered only a recent window. */
  notice?: string;
  /** File or run identity rendered in the viewer's title bar. */
  title?: string;
  /** When set, a follow toggle appears in the toolbar (business logs). */
  follow?: { active: boolean; onToggle: () => void };
  /** Embedded mode: no outer border/radius (a parent panel supplies them). */
  bare?: boolean;
  /** Tail control for file-backed views: 0 means the whole file. */
  tail?: { value: number; onChange: (n: number) => void };
  className?: string;
}

const AT_BOTTOM_THRESHOLD_PX = 24;

// renderLine splits line at every case-insensitive occurrence of query and
// marks matches; the match whose global ordinal equals current gets the
// stronger highlight.
function renderLine(line: string, query: string, current: number, ordinalBase: number, caseSensitive = false) {
  if (!query) return { node: line, matches: 0 };
  const haystack = caseSensitive ? line : line.toLowerCase();
  const q = caseSensitive ? query : query.toLowerCase();
  const parts: React.ReactNode[] = [];
  let i = 0;
  let matches = 0;
  for (;;) {
    const hit = haystack.indexOf(q, i);
    if (hit < 0) break;
    if (hit > i) parts.push(line.slice(i, hit));
    const ordinal = ordinalBase + matches;
    parts.push(
      <mark
        key={hit}
        data-ordinal={ordinal}
        className={cn(
          "rounded-sm px-0.5",
          ordinal === current
            ? "bg-warning-bg text-foreground ring-1 ring-warning-border"
            : "bg-warning-bg/60 text-foreground",
        )}
      >
        {line.slice(hit, hit + q.length)}
      </mark>,
    );
    matches++;
    i = hit + q.length;
  }
  if (matches === 0) return { node: line, matches: 0 };
  parts.push(line.slice(i));
  return { node: <>{parts}</>, matches };
}

// LEVEL_RE picks a log level out of a line for tone coloring.
const LEVEL_RE = /\b(FATAL|ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)\b/;

function lineTone(line: string): string | undefined {
  const m = LEVEL_RE.exec(line);
  switch (m?.[1]) {
    case "FATAL":
    case "ERROR":
      return "text-destructive";
    case "WARN":
    case "WARNING":
      return "text-warning";
    case "INFO":
      return "text-primary";
    default:
      return undefined;
  }
}

// renderLines renders every line with match highlights and returns the total
// match count. Module-level so render-loop purity rules don't apply.
function renderLines(lines: string[], query: string, current: number, caseSensitive: boolean) {
  const rendered = [];
  let base = 0;
  for (let i = 0; i < lines.length; i++) {
    const text = lines[i] ?? "";
    const { node, matches } = renderLine(text, query, current, base, caseSensitive);
    rendered.push({ no: i + 1, node, tone: lineTone(text) });
    base += matches;
  }
  return { rendered, totalMatches: base };
}

// LogViewer is the console's log panel: a light surface matched to the rest
// of the console, line numbers, in-place search with match navigation
// (Enter/Shift+Enter jumps between highlighted matches without hiding
// context), wrap toggle, copy and download. It sticks to the bottom while
// streaming until the user scrolls up.
export default function LogViewer({
  lines,
  status,
  emptyText,
  downloadName = "log.txt",
  onDownload,
  notice,
  title,
  follow,
  bare,
  tail,
  className,
}: LogViewerProps) {
  const { t } = useTranslation();
  const boxRef = useRef<HTMLDivElement>(null);
  const [stick, setStick] = useState(true);
  const [query, setQuery] = useState("");
  const [wrap, setWrap] = useState(true);
  const [matchIdx, setMatchIdx] = useState(0);
  const [caseSensitive, setCaseSensitive] = useState(false);
  // Highlighting is derived from a deferred query so typing stays responsive
  // while a large buffer re-renders its match spans.
  const deferredQuery = useDeferredValue(query);

  useEffect(() => {
    const el = boxRef.current;
    if (stick && el) {
      el.scrollTop = el.scrollHeight;
    }
  }, [lines, stick]);

  const onScroll = () => {
    const el = boxRef.current;
    if (!el) return;
    setStick(el.scrollHeight - el.scrollTop - el.clientHeight < AT_BOTTOM_THRESHOLD_PX);
  };

  const copyText = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast(t("logViewer.copied"));
    } catch {
      // Clipboard unavailable (permissions or insecure context): nothing to do.
    }
  };

  const download = () => {
    const blob = new Blob([lines.join("\n")], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = downloadName;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url));
  };

  const scrollToBottom = () => {
    setStick(true);
    const el = boxRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  };

  // Match ordinals across all lines, for the counter and current highlight.
  const { rendered, totalMatches } = useMemo(
    () => renderLines(lines, deferredQuery.trim(), matchIdx, caseSensitive),
    [lines, deferredQuery, matchIdx, caseSensitive],
  );

  // Clamp the current match when the result set shrinks.
  const current = totalMatches === 0 ? 0 : Math.min(matchIdx, totalMatches - 1);

  // Scroll the current match into view when it changes. The programmatic
  // scroll fires onScroll, which disengages stick-to-bottom on its own.
  useEffect(() => {
    if (totalMatches === 0) return;
    const el = boxRef.current?.querySelector(`mark[data-ordinal="${current}"]`);
    el?.scrollIntoView({ block: "center" });
  }, [current, totalMatches]);

  const jump = (delta: number) => {
    if (totalMatches === 0) return;
    setMatchIdx(((current + delta) % totalMatches + totalMatches) % totalMatches);
  };

  const toolbarButton =
    "inline-flex h-8 items-center gap-1.5 rounded-md border border-log-line bg-card px-2.5 text-[12px] text-log-foreground hover:bg-secondary disabled:pointer-events-none disabled:opacity-40";

  return (
    <div className={cn("relative", className)}>
      <div className={cn("overflow-hidden bg-log text-log-foreground", !bare && "rounded-md border border-log-line")}>
        {title && (
          <div className="flex items-center gap-2 border-b border-log-line px-3 py-2">
            <span className="truncate font-mono text-[13px] text-log-foreground">{title}</span>
            {lines.length > 0 && (
              <span className="ml-auto flex-none font-mono text-[11px] text-log-muted">
                {t("logViewer.lineCount", { count: lines.length })}
              </span>
            )}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2 border-b border-log-line bg-secondary/50 px-2 py-1.5">
          <div className="flex min-w-0 flex-1 items-center gap-1.5 rounded border border-log-line bg-card px-2">
            <Search size={12} className="flex-none text-log-muted" aria-hidden="true" />
            <input
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setMatchIdx(0);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  jump(e.shiftKey ? -1 : 1);
                }
              }}
              placeholder={t("logViewer.searchPlaceholder")}
              className="h-8 w-full bg-transparent text-[12px] text-foreground outline-none placeholder:text-log-muted"
            />
            {query && (
              <>
                <span className="flex-none font-mono text-[11px] text-log-muted">
                  {totalMatches === 0 ? "0/0" : `${current + 1}/${totalMatches}`}
                </span>
                <button
                  type="button"
                  aria-label={t("logViewer.prevMatch")}
                  onClick={() => jump(-1)}
                  className="flex-none rounded p-0.5 text-log-muted hover:text-foreground"
                >
                  <ChevronUp size={12} aria-hidden="true" />
                </button>
                <button
                  type="button"
                  aria-label={t("logViewer.nextMatch")}
                  onClick={() => jump(1)}
                  className="flex-none rounded p-0.5 text-log-muted hover:text-foreground"
                >
                  <ChevronDown size={12} aria-hidden="true" />
                </button>
                <button
                  type="button"
                  aria-label={t("logViewer.clearSearch")}
                  onClick={() => {
                    setQuery("");
                    setMatchIdx(0);
                  }}
                  className="flex-none rounded p-0.5 text-log-muted hover:text-foreground"
                >
                  <X size={12} aria-hidden="true" />
                </button>
              </>
            )}
          </div>
          {follow && (
            <button
              type="button"
              aria-pressed={follow.active}
              onClick={follow.onToggle}
              className={cn(
                toolbarButton,
                follow.active && "border-primary/60 bg-accent text-primary",
              )}
            >
              {t("instances.detail.follow")}
            </button>
          )}
          <button
            type="button"
            aria-label={t("logViewer.caseSensitive")}
            aria-pressed={caseSensitive}
            onClick={() => {
              setCaseSensitive((v) => !v);
              setMatchIdx(0);
            }}
            className={cn(toolbarButton, caseSensitive && "bg-secondary text-foreground")}
          >
            Aa
          </button>
          {tail && (
            <select
              aria-label={t("logViewer.tailLines")}
              value={tail.value}
              onChange={(e) => tail.onChange(Number(e.target.value))}
              className="h-8 rounded-md border border-log-line bg-card px-2 text-[12px] text-foreground"
            >
              {[200, 500, 1000, 0].map((n) => (
                <option key={n} value={n} className="text-black">
                  {n === 0 ? t("logViewer.tailAll") : t("logViewer.tailN", { n })}
                </option>
              ))}
            </select>
          )}
          <button
            type="button"
            aria-pressed={wrap}
            onClick={() => setWrap((v) => !v)}
            className={cn(toolbarButton, wrap && "bg-secondary text-foreground")}
          >
            <TextWrap size={12} aria-hidden="true" />
            {t("logViewer.wrap")}
          </button>
          <button
            type="button"
            onClick={() => void copyText(lines.join("\n"))}
            disabled={lines.length === 0}
            className={toolbarButton}
          >
            <Copy size={12} aria-hidden="true" />
            {t("logViewer.copyAll")}
          </button>
          <button
            type="button"
            onClick={onDownload ?? download}
            disabled={lines.length === 0 && !onDownload}
            className={toolbarButton}
          >
            <Download size={12} aria-hidden="true" />
            {t("logViewer.download")}
          </button>
          {status && (
            <span className="ml-auto flex flex-none items-center gap-1.5 text-[12px] text-log-muted">
              {t("logViewer.finished")}
              <StatusBadge status={status} />
            </span>
          )}
        </div>
        <div
          ref={boxRef}
          onScroll={onScroll}
          data-testid="log-box"
          className="h-[clamp(300px,44dvh,560px)] overflow-auto bg-log py-1.5 font-mono text-[13px] leading-[1.65] text-log-foreground max-sm:h-[60dvh]"
        >
          {notice && (
            <p className="border-b border-warning-border bg-warning-bg px-3 py-1.5 font-sans text-[12px] text-warning">
              {notice}
            </p>
          )}
          {lines.length === 0 ? (
            <p className="p-3 font-sans text-log-muted">
              {emptyText ?? t("logViewer.empty")}
            </p>
          ) : (
            <>
              {totalMatches === 0 && deferredQuery.trim() !== "" && (
                <p className="px-3 pt-1 font-sans text-[12px] text-log-muted">
                  {t("logViewer.noMatches")}
                </p>
              )}
              {rendered.map(({ no, node, tone }) => (
                <div
                  key={no}
                  className="group flex items-start [contain-intrinsic-size:20px] [content-visibility:auto]"
                >
                  <span className="w-11 flex-none pr-3 text-right text-log-muted select-none">
                    {no}
                  </span>
                  <span
                    className={cn(
                      "flex-1",
                      wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre",
                      tone,
                    )}
                  >
                    {node}
                  </span>
                  <button
                    type="button"
                    aria-label={t("logViewer.copyLine")}
                    onClick={() => void copyText(lines[no - 1] ?? "")}
                    className="mx-1 shrink-0 self-center rounded p-0.5 text-log-muted opacity-0 transition-opacity group-hover:opacity-100 hover:text-foreground max-sm:opacity-100"
                  >
                    <Copy size={11} aria-hidden="true" />
                  </button>
                </div>
              ))}
            </>
          )}
        </div>
      </div>
      {!stick && (
        <button
          type="button"
          onClick={scrollToBottom}
          className="absolute right-3 bottom-3 rounded-full border bg-card px-3 py-1.5 text-[12px] text-foreground shadow-sm"
        >
          ↓ {t("logViewer.backToBottom")}
        </button>
      )}
    </div>
  );
}
