import type { ReactNode } from "react";

interface EmptyStateProps {
  icon: ReactNode;
  title: string;
  description: string;
  action?: ReactNode;
  compact?: boolean;
}

// EmptyState is the shared first-run and zero-data surface: a quiet icon
// tile, a short explanation, and at most one or two next actions.
export default function EmptyState({
  icon,
  title,
  description,
  action,
  compact = false,
}: EmptyStateProps) {
  return (
    <div
      className={
        compact
          ? "flex flex-col items-center justify-center px-6 py-10 text-center"
          : "flex min-h-[360px] flex-col items-center justify-center rounded-md border border-dashed bg-card/60 px-8 py-14 text-center"
      }
    >
      <div className="grid h-12 w-12 place-items-center rounded-md border bg-card text-muted-foreground">
        {icon}
      </div>
      <h2 className="mt-4 text-lg font-semibold tracking-tight">{title}</h2>
      <p className="mt-2 max-w-md text-sm leading-6 text-muted-foreground">{description}</p>
      {action && <div className="mt-5 flex flex-wrap justify-center gap-2">{action}</div>}
    </div>
  );
}
