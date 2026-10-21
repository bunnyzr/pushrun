import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { validateEnvRows, type EnvIssue, type EnvRow } from "@/lib/project-edit";
import { cn } from "@/lib/utils";

export interface EnvEditorProps {
  value: Record<string, string> | undefined;
  onChange: (env: Record<string, string> | undefined) => void;
  // Row-level issues (invalid AND duplicate keys) are reported up so the page
  // can block the save; the record pushed via onChange cannot represent
  // duplicates.
  onIssuesChange?: (issues: EnvIssue[]) => void;
}

function rowsOf(env: Record<string, string> | undefined): EnvRow[] {
  return Object.entries(env ?? {}).map(([key, value]) => ({ key, value }));
}

function recordOf(rows: EnvRow[]): Record<string, string> | undefined {
  const env: Record<string, string> = {};
  for (const row of rows) {
    if (row.key !== "") env[row.key] = row.value;
  }
  return Object.keys(env).length > 0 ? env : undefined;
}

// Project-wide environment. Rows are local state so half-typed keys and
// duplicate rows survive re-renders; every change pushes the clean record to
// the draft. The parent remounts this card per project selection.
export default function EnvEditor({ value, onChange, onIssuesChange }: EnvEditorProps) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<EnvRow[]>(() => rowsOf(value));
  const issues = validateEnvRows(rows);
  const issueAt = (index: number) => issues.find((i) => i.index === index);

  useEffect(() => {
    onIssuesChange?.(validateEnvRows(rows));
  }, [rows, onIssuesChange]);

  const push = (next: EnvRow[]) => {
    setRows(next);
    onChange(recordOf(next));
  };

  return (
    <section aria-label={t("projects.env.title")} className="rounded-lg border bg-card p-3">
      <div className="mb-2 flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t("projects.env.title")}</h3>
        <Button
          size="sm"
          variant="outline"
          className="ml-auto"
          onClick={() => push([...rows, { key: "", value: "" }])}
        >
          {t("projects.env.add")}
        </Button>
      </div>
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("projects.env.empty")}</p>
      ) : (
        <div className="space-y-1.5">
          {rows.map((row, i) => {
            const issue = issueAt(i);
            return (
              <div key={i} className="flex items-center gap-2">
                <Input
                  aria-label={`${t("projects.env.keyPlaceholder")} ${i + 1}`}
                  placeholder={t("projects.env.keyPlaceholder")}
                  value={row.key}
                  aria-invalid={issue !== undefined}
                  onChange={(e) =>
                    push(rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)))
                  }
                  className={cn("w-48 font-mono", issue && "border-destructive")}
                />
                <Input
                  aria-label={`${t("projects.env.valuePlaceholder")} ${i + 1}`}
                  placeholder={t("projects.env.valuePlaceholder")}
                  value={row.value}
                  onChange={(e) =>
                    push(rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))
                  }
                  className="min-w-0 flex-1 font-mono"
                />
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`${t("projects.env.remove")} ${i + 1}`}
                  onClick={() => push(rows.filter((_, j) => j !== i))}
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
