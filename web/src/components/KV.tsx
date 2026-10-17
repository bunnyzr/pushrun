import type { ReactNode } from "react";

// KV renders one label/value pair of a detail grid; an absent value shows an
// em dash.
export default function KV({ label, children }: { label: string; children?: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-0.5 flex min-w-0 items-center gap-1 text-sm">{children ?? "—"}</div>
    </div>
  );
}
