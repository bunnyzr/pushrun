import { useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { dismissToast, getToasts, subscribeToasts } from "@/lib/toast";
import { cn } from "@/lib/utils";

export default function Toaster() {
  const { t } = useTranslation();
  const items = useSyncExternalStore(subscribeToasts, getToasts);
  return (
    <div className="fixed right-4 bottom-4 z-50 flex w-80 flex-col gap-2">
      {items.map((item) => (
        <div
          key={item.id}
          role="status"
          className={cn(
            "rounded-md border bg-card px-4 py-3 text-sm shadow-lg",
            item.variant === "error" && "border-destructive text-destructive",
          )}
        >
          <div className="flex items-start justify-between gap-2">
            <span className="break-words">{item.message}</span>
            <button
              type="button"
              aria-label={t("common.dismiss")}
              onClick={() => dismissToast(item.id)}
              className="shrink-0 text-muted-foreground hover:text-foreground"
            >
              ×
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}
