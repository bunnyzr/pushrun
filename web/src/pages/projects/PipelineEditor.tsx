import { useTranslation } from "react-i18next";
import { ArrowDown, ArrowUp, Trash2 } from "lucide-react";
import ScriptEditor from "@/components/ScriptEditor";
import SectionCard from "@/components/SectionCard";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import type { Health, Step } from "@/lib/types";

const HEALTH_TYPES = ["tcp", "http", "command"] as const;

export interface PipelineEditorProps {
  onShowEnvHelp?: () => void;
  steps: Step[];
  onChange: (steps: Step[]) => void;
}

export default function PipelineEditor({ onShowEnvHelp, steps, onChange }: PipelineEditorProps) {
  const { t } = useTranslation();

  const update = (i: number, patch: Partial<Step>) => {
    onChange(steps.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  };

  const updateHealth = (i: number, patch: Partial<Health>) => {
    const step = steps[i];
    if (!step) return;
    const next: Health = { type: "", target: "", ...step.health, ...patch };
    update(i, { health: next.type === "" ? undefined : next });
  };

  const remove = (i: number) => {
    onChange(steps.filter((_, j) => j !== i));
  };

  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= steps.length) return;
    const next = [...steps];
    next[i] = steps[j]!;
    next[j] = steps[i]!;
    onChange(next);
  };

  const addStep = () => {
    const step: Step = { name: "", run: "" };
    const last = steps[steps.length - 1];
    // A trailing background step must stay last, so insert before it.
    if (last?.background) {
      onChange([...steps.slice(0, -1), step, last]);
    } else {
      onChange([...steps, step]);
    }
  };

  const numberOrUndefined = (value: string): number | undefined =>
    value === "" ? undefined : Number(value);

  return (
    <SectionCard
      title={t("projects.pipeline.title")}
      description={t("projects.pipeline.description")}
      action={
        <div className="flex items-center gap-2">
          {onShowEnvHelp && (
            <button
              type="button"
              onClick={onShowEnvHelp}
              className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              {t("projects.envHelp.trigger")}
            </button>
          )}
          <Button size="sm" variant="outline" onClick={addStep}>
            {t("projects.pipeline.add")}
          </Button>
        </div>
      }
    >
      {steps.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
          {t("projects.pipeline.empty")}
        </p>
      ) : (
        <div className="space-y-3">
          {steps.map((step, i) => {
            const isLast = i === steps.length - 1;
            return (
              <div key={i} className="rounded-md border bg-secondary/30 p-3">
                <div className="flex flex-wrap items-end gap-3">
                  <span className="mb-2.5 grid h-6 w-6 flex-none place-items-center rounded-full border bg-secondary font-mono text-[11px] font-semibold text-foreground">
                    {i + 1}
                  </span>
                  <div className="min-w-0 flex-1 space-y-1 sm:max-w-52">
                    <Label htmlFor={`step-name-${i}`} className="text-[11px] text-muted-foreground">
                      {t("projects.pipeline.name")}
                    </Label>
                    <Input
                      id={`step-name-${i}`}
                      aria-label={`${t("projects.pipeline.name")} ${i + 1}`}
                      value={step.name}
                      onChange={(e) => update(i, { name: e.target.value })}
                    />
                  </div>
                  <div className="w-28 space-y-1">
                    <Label htmlFor={`step-timeout-${i}`} className="text-[11px] text-muted-foreground">
                      {t("projects.pipeline.timeout")}
                    </Label>
                    <Input
                      id={`step-timeout-${i}`}
                      aria-label={`${t("projects.pipeline.timeout")} ${i + 1}`}
                      type="number"
                      min={0}
                      value={step.timeout ?? ""}
                      onChange={(e) => update(i, { timeout: numberOrUndefined(e.target.value) })}
                    />
                  </div>
                  {(isLast || step.background) && (
                    <div className="flex items-center gap-2 pb-2">
                      <Switch
                        id={`step-background-${i}`}
                        checked={step.background === true}
                        onCheckedChange={(v) => update(i, { background: v || undefined })}
                      />
                      <Label htmlFor={`step-background-${i}`} className="text-xs">
                        {t("projects.pipeline.background")}
                      </Label>
                    </div>
                  )}
                  <span className="ml-auto flex items-center gap-1 pb-0.5">
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={`${t("projects.pipeline.moveUp")} ${i + 1}`}
                      disabled={i === 0 || step.background === true}
                      onClick={() => move(i, -1)}
                    >
                      <ArrowUp className="h-4 w-4" />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={`${t("projects.pipeline.moveDown")} ${i + 1}`}
                      disabled={isLast || steps[i + 1]?.background === true}
                      onClick={() => move(i, 1)}
                    >
                      <ArrowDown className="h-4 w-4" />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={`${t("projects.pipeline.delete")} ${i + 1}`}
                      onClick={() => remove(i)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </span>
                </div>
                <div className="mt-3 space-y-1">
                  <Label className="text-[11px] text-muted-foreground">
                    {t("projects.pipeline.run")}
                  </Label>
                  <ScriptEditor
                    aria-label={`${t("projects.pipeline.run")} ${i + 1}`}
                    value={step.run}
                    placeholder={t("projects.pipeline.scriptPlaceholder")}
                    onChange={(run) => update(i, { run })}
                  />
                </div>
                {step.background && (
                  <div className="mt-3 rounded-md border bg-card p-3">
                    <p className="text-xs leading-5 text-muted-foreground">
                      {step.health
                        ? t("projects.pipeline.healthExplainer")
                        : t("projects.pipeline.healthAuto")}
                    </p>
                    <div className="mt-2 flex flex-wrap items-center gap-2">
                      <Select
                        aria-label={`${t("projects.pipeline.healthType")} ${i + 1}`}
                        value={step.health?.type ?? ""}
                        onChange={(e) => updateHealth(i, { type: e.target.value })}
                        className="w-40"
                      >
                        <option value="">{t("projects.pipeline.healthAutoOption")}</option>
                        <option value="none">{t("projects.pipeline.healthSkip")}</option>
                        {HEALTH_TYPES.map((type) => (
                          <option key={type} value={type}>
                            {type}
                          </option>
                        ))}
                      </Select>
                      {step.health && step.health.type !== "none" && (
                        <>
                          <Input
                            aria-label={`${t("projects.pipeline.healthTarget")} ${i + 1}`}
                            placeholder={t("projects.pipeline.healthTarget")}
                            value={step.health.target}
                            onChange={(e) => updateHealth(i, { target: e.target.value })}
                            className="min-w-0 flex-1 font-mono"
                          />
                          <Input
                            aria-label={`${t("projects.pipeline.healthInterval")} ${i + 1}`}
                            placeholder={t("projects.pipeline.healthInterval")}
                            type="number"
                            min={0}
                            value={step.health.interval ?? ""}
                            onChange={(e) =>
                              updateHealth(i, { interval: numberOrUndefined(e.target.value) })
                            }
                            className="w-28"
                          />
                          <Input
                            aria-label={`${t("projects.pipeline.healthRetries")} ${i + 1}`}
                            placeholder={t("projects.pipeline.healthRetries")}
                            type="number"
                            min={0}
                            value={step.health.retries ?? ""}
                            onChange={(e) =>
                              updateHealth(i, { retries: numberOrUndefined(e.target.value) })
                            }
                            className="w-28"
                          />
                        </>
                      )}
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </SectionCard>
  );
}
