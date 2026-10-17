import { useTranslation } from "react-i18next";
import { ArrowDown, ArrowUp, Trash2 } from "lucide-react";
import ScriptEditor from "@/components/ScriptEditor";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import type { Health, Step } from "@/lib/types";

const HEALTH_TYPES = ["tcp", "http", "command"] as const;

export interface PipelineEditorProps {
  steps: Step[];
  onChange: (steps: Step[]) => void;
}

export default function PipelineEditor({ steps, onChange }: PipelineEditorProps) {
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
    <section aria-label={t("projects.pipeline.title")} className="rounded-lg border bg-card p-3">
      <div className="mb-2 flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t("projects.pipeline.title")}</h3>
        <Button size="sm" variant="outline" className="ml-auto" onClick={addStep}>
          {t("projects.pipeline.add")}
        </Button>
      </div>
      {steps.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("projects.pipeline.empty")}</p>
      ) : (
        <div className="space-y-2">
          {steps.map((step, i) => {
            const isLast = i === steps.length - 1;
            return (
              <div key={i} className="space-y-1.5 rounded-md border bg-background p-2">
                <div className="flex flex-wrap items-center gap-2">
                  <Input
                    aria-label={`${t("projects.pipeline.name")} ${i + 1}`}
                    placeholder={t("projects.pipeline.name")}
                    value={step.name}
                    onChange={(e) => update(i, { name: e.target.value })}
                    className="w-36"
                  />
                  <Input
                    aria-label={`${t("projects.pipeline.timeout")} ${i + 1}`}
                    placeholder={t("projects.pipeline.timeout")}
                    type="number"
                    min={0}
                    value={step.timeout ?? ""}
                    onChange={(e) => update(i, { timeout: numberOrUndefined(e.target.value) })}
                    className="w-28"
                  />
                  {(isLast || step.background) && (
                    <span className="flex items-center gap-1.5">
                      <Switch
                        id={`step-background-${i}`}
                        checked={step.background === true}
                        onCheckedChange={(v) => update(i, { background: v || undefined })}
                      />
                      <Label htmlFor={`step-background-${i}`} className="text-xs">
                        {t("projects.pipeline.background")}
                      </Label>
                    </span>
                  )}
                  <span className="ml-auto flex items-center gap-1">
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
                <ScriptEditor
                  aria-label={`${t("projects.pipeline.run")} ${i + 1}`}
                  value={step.run}
                  onChange={(run) => update(i, { run })}
                />
                {step.background && (
                  <div className="flex items-center gap-2">
                    <Select
                      aria-label={`${t("projects.pipeline.healthType")} ${i + 1}`}
                      value={step.health?.type ?? ""}
                      onChange={(e) => updateHealth(i, { type: e.target.value })}
                      className="w-36"
                    >
                      <option value="">{t("projects.pipeline.healthNone")}</option>
                      {HEALTH_TYPES.map((type) => (
                        <option key={type} value={type}>
                          {type}
                        </option>
                      ))}
                    </Select>
                    {step.health && (
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
                )}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
