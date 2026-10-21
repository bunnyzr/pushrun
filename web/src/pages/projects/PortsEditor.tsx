import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { validatePortNames } from "@/lib/project-edit";
import { cn } from "@/lib/utils";

export interface PortsEditorProps {
  value: string[] | undefined;
  onChange: (ports: string[] | undefined) => void;
}

// Named leased ports. Names are local state so duplicates survive re-renders;
// every change pushes the clean list to the draft (undefined when empty).
export default function PortsEditor({ value, onChange }: PortsEditorProps) {
  const { t } = useTranslation();
  const [names, setNames] = useState<string[]>(() => value ?? []);
  const issues = validatePortNames(names);
  const issueAt = (index: number) => issues.find((i) => i.index === index);

  const push = (next: string[]) => {
    setNames(next);
    const clean = next.filter((n) => n !== "");
    onChange(clean.length > 0 ? clean : undefined);
  };

  return (
    <section aria-label={t("projects.ports.title")} className="rounded-lg border bg-card p-3">
      <div className="mb-2 flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t("projects.ports.title")}</h3>
        <Button
          size="sm"
          variant="outline"
          className="ml-auto"
          onClick={() => push([...names, ""])}
        >
          {t("projects.ports.add")}
        </Button>
      </div>
      <p className="mb-2 text-xs text-muted-foreground">{t("projects.ports.usageHint")}</p>
      {names.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("projects.ports.emptyHint")}</p>
      ) : (
        <div className="space-y-1.5">
          {names.map((name, i) => {
            const issue = issueAt(i);
            return (
              <div key={i} className="flex items-center gap-2">
                <Input
                  aria-label={`${t("projects.ports.namePlaceholder")} ${i + 1}`}
                  placeholder={t("projects.ports.namePlaceholder")}
                  value={name}
                  aria-invalid={issue !== undefined}
                  onChange={(e) => push(names.map((n, j) => (j === i ? e.target.value : n)))}
                  className={cn("w-48 font-mono", issue && "border-destructive")}
                />
                {name.trim() !== "" && (
                  <code className="rounded bg-secondary px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
                    {"$CI_PORT_" + name.trim().toUpperCase()}
                  </code>
                )}
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`${t("projects.ports.remove")} ${i + 1}`}
                  onClick={() => push(names.filter((_, j) => j !== i))}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
