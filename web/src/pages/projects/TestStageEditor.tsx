import { useState } from "react";
import { useTranslation } from "react-i18next";
import ScriptEditor from "@/components/ScriptEditor";
import { Input } from "@/components/ui/input";
import type { Step } from "@/lib/types";

export interface TestStageEditorProps {
  value: Step | undefined;
  onChange: (step: Step | undefined) => void;
}

// The dedicated test step. Fields are local state (the parent remounts this
// card per project selection) so a whitespace-only script is kept as typed
// and name/timeout edits survive while the script is empty; the parent only
// hears a step once the script has non-whitespace content, and undefined
// otherwise.
export default function TestStageEditor({ value, onChange }: TestStageEditorProps) {
  const { t } = useTranslation();
  const [name, setName] = useState(value?.name ?? "test");
  const [run, setRun] = useState(value?.run ?? "");
  const [timeout, setTimeout] = useState<number | undefined>(value?.timeout);

  const push = (next: { name: string; run: string; timeout: number | undefined }) => {
    setName(next.name);
    setRun(next.run);
    setTimeout(next.timeout);
    if (next.run.trim() === "") {
      onChange(undefined);
      return;
    }
    const step: Step = { name: next.name, run: next.run };
    if (next.timeout !== undefined) step.timeout = next.timeout;
    onChange(step);
  };

  return (
    <section aria-label={t("projects.test.title")} className="rounded-lg border bg-card p-3">
      <h3 className="mb-2 text-sm font-semibold">{t("projects.test.title")}</h3>
      <div className="mb-1.5 flex flex-wrap items-center gap-2">
        <Input
          aria-label={t("projects.test.name")}
          placeholder={t("projects.test.name")}
          value={name}
          onChange={(e) => push({ name: e.target.value, run, timeout })}
          className="w-36"
        />
        <Input
          aria-label={t("projects.test.timeout")}
          placeholder={t("projects.test.timeout")}
          type="number"
          min={0}
          value={timeout ?? ""}
          onChange={(e) =>
            push({
              name,
              run,
              timeout: e.target.value === "" ? undefined : Number(e.target.value),
            })
          }
          className="w-28"
        />
        <span className="ml-auto text-xs text-muted-foreground">
          {t("projects.test.emptyHint")}
        </span>
      </div>
      <ScriptEditor
        aria-label={t("projects.test.run")}
        value={run}
        onChange={(next) => push({ name, run: next, timeout })}
      />
    </section>
  );
}
