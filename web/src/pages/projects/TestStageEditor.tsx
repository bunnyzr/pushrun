import { useState } from "react";
import { useTranslation } from "react-i18next";
import ScriptEditor from "@/components/ScriptEditor";
import SectionCard from "@/components/SectionCard";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
    <SectionCard title={t("projects.test.title")} description={t("projects.test.description")}>
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-40 space-y-1">
          <Label htmlFor="test-name" className="text-[11px] text-muted-foreground">
            {t("projects.test.name")}
          </Label>
          <Input
            id="test-name"
            aria-label={t("projects.test.name")}
            value={name}
            onChange={(e) => push({ name: e.target.value, run, timeout })}
          />
        </div>
        <div className="w-28 space-y-1">
          <Label htmlFor="test-timeout" className="text-[11px] text-muted-foreground">
            {t("projects.test.timeout")}
          </Label>
          <Input
            id="test-timeout"
            aria-label={t("projects.test.timeout")}
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
          />
        </div>
        <span className="pb-2.5 text-xs text-muted-foreground">
          {t("projects.test.emptyHint")}
        </span>
      </div>
      <div className="mt-3 space-y-1">
        <Label className="text-[11px] text-muted-foreground">{t("projects.test.run")}</Label>
        <ScriptEditor
          aria-label={t("projects.test.run")}
          value={run}
          placeholder={t("projects.test.scriptPlaceholder")}
          onChange={(next) => push({ name, run: next, timeout })}
        />
      </div>
    </SectionCard>
  );
}
