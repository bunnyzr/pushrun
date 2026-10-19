import { useTranslation } from "react-i18next";
import { Input } from "@/components/ui/input";
import { isValidLogDir } from "@/lib/project-edit";

export interface LogDirEditorProps {
  value: string | undefined;
  onChange: (logDir: string | undefined) => void;
}

// The instance business-log directory; empty means the server default.
export default function LogDirEditor({ value, onChange }: LogDirEditorProps) {
  const { t } = useTranslation();
  const dir = value ?? "";
  const valid = isValidLogDir(dir);
  return (
    <section aria-label={t("projects.logdir.title")} className="rounded-lg border bg-card p-3">
      <h3 className="mb-2 text-sm font-semibold">{t("projects.logdir.title")}</h3>
      <Input
        aria-label={t("projects.logdir.title")}
        placeholder="logs"
        value={dir}
        aria-invalid={!valid}
        onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
        className="font-mono"
      />
      <p className={valid ? "mt-1.5 text-xs text-muted-foreground" : "mt-1.5 text-xs text-destructive"}>
        {valid ? t("projects.logdir.hint") : t("projects.issues.invalid_log_dir")}
      </p>
    </section>
  );
}
