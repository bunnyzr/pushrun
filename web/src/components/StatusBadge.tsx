import { useTranslation } from "react-i18next";
import { Badge, type BadgeProps } from "@/components/ui/badge";

const STATUS_VARIANTS: Record<string, BadgeProps["variant"]> = {
  RUNNING: "running",
  STOPPED: "muted",
  SUCCESS: "success",
  FAILED: "destructive",
};

export default function StatusBadge({ status, className }: { status: string; className?: string }) {
  const { t } = useTranslation();
  const variant = STATUS_VARIANTS[status] ?? "muted";
  const label = t(`status.${status.toLowerCase()}`, { defaultValue: status });
  return (
    <Badge variant={variant} className={className}>
      {label}
    </Badge>
  );
}
