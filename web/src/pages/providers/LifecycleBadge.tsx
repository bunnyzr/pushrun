import { CircleDashed, Flame, LoaderCircle, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Badge, type BadgeProps } from "@/components/ui/badge";
import type { LifecycleStatus } from "./lifecycle";

const TONES: Record<LifecycleStatus, { variant: BadgeProps["variant"]; icon: LucideIcon }> = {
  warm: { variant: "success", icon: Flame },
  warming: { variant: "warning", icon: LoaderCircle },
  cold: { variant: "muted", icon: CircleDashed },
};

// LifecycleBadge is the pill badge for a provider's warmup lifecycle.
export default function LifecycleBadge({ status }: { status: LifecycleStatus }) {
  const { t } = useTranslation();
  const tone = TONES[status];
  const Icon = tone.icon;
  return (
    <Badge variant={tone.variant}>
      <Icon
        size={13}
        aria-hidden="true"
        className={status === "warming" ? "animate-spin" : undefined}
      />
      {t(`providers.card.status.${status}`)}
    </Badge>
  );
}
