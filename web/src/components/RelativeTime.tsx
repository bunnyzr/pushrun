import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";

const TICK_MS = 30_000;

function formatRelative(ts: string, locale: string): string {
  const time = new Date(ts).getTime();
  if (Number.isNaN(time)) return ts;
  const diffSec = Math.round((time - Date.now()) / 1000);
  const abs = Math.abs(diffSec);
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (abs < 45) return rtf.format(diffSec, "second");
  if (abs < 3600) return rtf.format(Math.round(diffSec / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diffSec / 3600), "hour");
  if (abs < 86400 * 30) return rtf.format(Math.round(diffSec / 86400), "day");
  return new Date(time).toLocaleDateString(locale);
}

// RelativeTime renders "3 minutes ago"-style text and refreshes itself.
export default function RelativeTime({ ts, className }: { ts?: string; className?: string }) {
  const { i18n } = useTranslation();
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), TICK_MS);
    return () => clearInterval(id);
  }, []);
  if (!ts) return null;
  return (
    <span className={cn("text-muted-foreground", className)} title={new Date(ts).toLocaleString()}>
      {formatRelative(ts, i18n.language)}
    </span>
  );
}
