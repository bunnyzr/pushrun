import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

interface SectionCardProps {
  title: string;
  description?: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}

// SectionCard is the common editor surface: a titled card with an optional
// one-line explanation and a top-right action. Keeping the heading rhythm in
// one place stops the editor pages from drifting into bare form grids.
export default function SectionCard({
  title,
  description,
  action,
  children,
  className,
}: SectionCardProps) {
  return (
    <section aria-label={title} className={cn("rounded-md border bg-card p-4", className)}>
      <div className="flex flex-wrap items-start gap-2">
        <div className="min-w-0 flex-1">
          <h3 className="text-[15px] font-semibold tracking-[-0.01em]">{title}</h3>
          {description && (
            <p className="mt-1 text-[11.5px] leading-5 text-muted-foreground">{description}</p>
          )}
        </div>
        {action}
      </div>
      <div className="mt-3">{children}</div>
    </section>
  );
}
