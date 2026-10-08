import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import StatusBadge from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { toast } from "@/lib/toast";
import { cn } from "@/lib/utils";

interface LogViewerProps {
  lines: string[];
  /** Terminal status of the stream, shown in the toolbar once known. */
  status?: string | null;
  emptyText?: string;
  downloadName?: string;
  className?: string;
}

const AT_BOTTOM_THRESHOLD_PX = 24;

export default function LogViewer({
  lines,
  status,
  emptyText,
  downloadName = "log.txt",
  className,
}: LogViewerProps) {
  const { t } = useTranslation();
  const boxRef = useRef<HTMLDivElement>(null);
  const [stick, setStick] = useState(true);

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
    URL.revokeObjectURL(url);
  };

  const scrollToBottom = () => {
    setStick(true);
    const el = boxRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  };

  return (
    <div className={cn("relative", className)}>
      <div className="mb-1 flex items-center gap-2">
        <Button variant="outline" size="sm" onClick={() => void copyText(lines.join("\n"))} disabled={lines.length === 0}>
          {t("logViewer.copyAll")}
        </Button>
        <Button variant="outline" size="sm" onClick={download} disabled={lines.length === 0}>
          {t("logViewer.download")}
        </Button>
        {status && (
          <span className="ml-auto flex items-center gap-1 text-xs text-muted-foreground">
            {t("logViewer.finished")}
            <StatusBadge status={status} />
          </span>
        )}
      </div>
      <div
        ref={boxRef}
        onScroll={onScroll}
        data-testid="log-box"
        className="h-72 overflow-auto rounded-md border bg-muted/40 p-2 font-mono text-xs leading-5"
      >
        {lines.length === 0 ? (
          <p className="p-2 font-sans text-muted-foreground">{emptyText ?? t("logViewer.empty")}</p>
        ) : (
          lines.map((line, i) => (
            <div key={i} className="group flex items-start gap-2">
              <span className="flex-1 whitespace-pre-wrap break-all">{line}</span>
              <button
                type="button"
                aria-label={t("logViewer.copyLine")}
                onClick={() => void copyText(line)}
                className="shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100 hover:text-foreground"
              >
                ⧉
              </button>
            </div>
          ))
        )}
      </div>
      {!stick && (
        <button
          type="button"
          onClick={scrollToBottom}
          className="absolute right-3 bottom-3 rounded-full border bg-card px-3 py-1 text-xs shadow"
        >
          ↓ {t("logViewer.backToBottom")}
        </button>
      )}
    </div>
  );
}
