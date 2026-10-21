import { CircleAlert, CircleCheck, CircleDashed, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

interface Tone {
  variant: "running" | "success" | "destructive" | "muted";
  icon: LucideIcon;
}

const STATUS_TONES: Record<string, Tone> = {
  RUNNING: { variant: "running", icon: CircleCheck },
  SUCCESS: { variant: "muted", icon: CircleCheck },
  FAILED: { variant: "destructive", icon: CircleAlert },
  STOPPED: { variant: "muted", icon: CircleDashed },
};

export default function StatusBadge({ status, className }: { status: string; className?: string }) {
  const { t } = useTranslation();
  const tone = STATUS_TONES[status] ?? { variant: "muted" as const, icon: CircleDashed };
  const label = t(`status.${status.toLowerCase()}`, { defaultValue: status });
  const Icon = tone.icon;
  return (
    <Badge variant={tone.variant} className={cn(className)}>
      <Icon size={13} aria-hidden="true" />
      {label}
    </Badge>
  );
}
