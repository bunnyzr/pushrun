import { Copy } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "@/lib/toast";
import { cn } from "@/lib/utils";

// CopyButton copies text to the clipboard, with a toast on success.
export default function CopyButton({
  text,
  className,
}: {
  text: string;
  className?: string;
}) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      aria-label={t("logViewer.copyLine")}
      onClick={() => {
        void navigator.clipboard
          .writeText(text)
          .then(() => toast(t("logViewer.copied")))
          .catch(() => {
            // Clipboard unavailable (permissions or insecure context).
          });
      }}
      className={cn(
        "inline-flex items-center rounded p-1 text-muted-foreground hover:bg-secondary hover:text-foreground",
        className,
      )}
    >
      <Copy size={13} aria-hidden="true" />
    </button>
  );
}
