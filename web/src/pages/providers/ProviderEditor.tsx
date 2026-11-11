import { useState } from "react";
import { useBlocker } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Plus, Trash2 } from "lucide-react";
import ScriptEditor from "@/components/ScriptEditor";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, apiFetch } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { Param, Provider } from "@/lib/types";

export interface ProviderEditorProps {
  // null means create; the parent mounts this dialog only while it is open,
  // so the initial state always comes from a fresh mount.
  provider: Provider | null;
  onClose: () => void;
}

const PARAM_ID_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

// The console always writes providers with the canonical bundle layout; the
// path fields in provider.yaml exist for imported multi-file bundles, not for
// console users to choose.
const DEFAULT_WARMUP_PATH = "scripts/warmup.sh";
const DEFAULT_INSTALL_PATH = "scripts/install.sh";

function blankParam(index: number, existing: Param[]): Param {
  const used = new Set(existing.map((p) => p.id.toLowerCase()));
  let n = index + 1;
  while (used.has(`param_${n}`)) n += 1;
  return { id: `param_${n}`, label: "", type: "string", scope: "warmup" };
}

// The editor carries the descriptor, parameter declarations, and script
// contents. Scripts are a map on the server, so the textareas preload from
// the single-provider GET; an empty textarea keeps server content only when
// the server had none — clearing a preloaded script sends "" to overwrite it.
export default function ProviderEditor({ provider, onClose }: ProviderEditorProps) {
  const { t } = useTranslation();
  const isCreate = provider === null;

  const detailQuery = useQuery({
    queryKey: ["providers", provider?.id ?? ""],
    queryFn: ({ signal }) =>
      apiFetch<Provider>(`/providers/${encodeURIComponent(provider?.id ?? "")}`, { signal }),
    enabled: !isCreate && provider?.scripts === undefined,
  });
  const scripts = provider?.scripts ?? detailQuery.data?.scripts ?? null;

  // A failed detail fetch must not block editing: without the script map,
  // the textareas stay empty and empty keeps the server content.
  if (!isCreate && scripts === null && !detailQuery.isError) {
    return (
      <Dialog
        open
        title={t("providers.editor.editTitle", { id: provider.id })}
        onClose={onClose}
        closeLabel={t("common.close")}
      >
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      </Dialog>
    );
  }
  return <ProviderEditorForm provider={provider} scripts={scripts} onClose={onClose} />;
}

function ProviderEditorForm({
  provider,
  scripts: serverScripts,
  onClose,
}: ProviderEditorProps & { scripts: Record<string, string> | null }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const isCreate = provider === null;
  const fieldPrefix = `provider-editor-${provider?.id ?? "new"}`;

  // Existing and imported providers keep the paths declared in their
  // provider.yaml; console-created providers always get the canonical ones.
  const warmupPath = provider?.warmup || DEFAULT_WARMUP_PATH;
  const installPath = provider?.install || DEFAULT_INSTALL_PATH;
  const scriptsLoaded = isCreate || serverScripts !== null;

  const [id, setId] = useState(provider?.id ?? "");
  const [name, setName] = useState(provider?.name ?? "");
  const [description, setDescription] = useState(provider?.description ?? "");
  const [warmupScript, setWarmupScript] = useState(
    provider?.warmup && serverScripts ? (serverScripts[provider.warmup] ?? "") : "",
  );
  const [installScript, setInstallScript] = useState(
    serverScripts?.[installPath] ?? "",
  );
  const [parameters, setParameters] = useState<Param[]>(provider?.parameters ?? []);
  const [error, setError] = useState<string | null>(null);
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  const current = {
    id,
    name,
    description,
    warmupScript,
    installScript,
    parameters,
  };
  const [initial] = useState(() => JSON.stringify(current));
  const dirty = JSON.stringify(current) !== initial;

  // Route changes are blocked while the modal has unsaved edits; the same
  // confirmation covers the close button and the backdrop.
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  );

  const requestClose = () => {
    if (dirty) {
      setConfirmDiscard(true);
      return;
    }
    onClose();
  };

  const discard = () => {
    setConfirmDiscard(false);
    if (blocker.state === "blocked") {
      blocker.proceed();
    } else {
      onClose();
    }
  };

  const cancelDiscard = () => {
    setConfirmDiscard(false);
    blocker.reset?.();
  };

  const updateParam = (index: number, patch: Partial<Param>) => {
    setParameters((current) =>
      current.map((parameter, i) => (i === index ? { ...parameter, ...patch } : parameter)),
    );
  };

  const removeParam = (index: number) => {
    setParameters((current) => current.filter((_, i) => i !== index));
  };

  const addParam = () => {
    setParameters((current) => [...current, blankParam(current.length, current)]);
  };

  const validate = (): string | null => {
    if (id.trim() === "" || name.trim() === "") {
      return t("providers.editor.missing");
    }
    // A failed script-detail fetch leaves the editors empty; saving then
    // keeps the stored files untouched instead of failing validation.
    if (scriptsLoaded && installScript.trim() === "") {
      return t("providers.editor.missing");
    }
    const seen = new Set<string>();
    for (const parameter of parameters) {
      const paramID = parameter.id.trim();
      if (!PARAM_ID_RE.test(paramID)) {
        return t("providers.editor.paramInvalid", { id: paramID || "?" });
      }
      if (seen.has(paramID)) {
        return t("providers.editor.paramDuplicate", { id: paramID });
      }
      if (!parameter.label?.trim()) {
        return t("providers.editor.paramLabelRequired", { id: paramID });
      }
      seen.add(paramID);
    }
    return null;
  };

  const saveMutation = useMutation({
    mutationFn: () => {
      // Clearing the warmup editor disables the phase; the install phase is
      // mandatory. When the script detail could not be loaded (only possible
      // while editing), leave both descriptor fields and files untouched.
      const hasWarmup = scriptsLoaded
        ? warmupScript.trim() !== ""
        : Boolean(provider?.warmup);
      const scripts: Record<string, string> = {};
      if (scriptsLoaded) {
        scripts[installPath] = installScript;
        if (hasWarmup) scripts[warmupPath] = warmupScript;
      }
      const body: Record<string, unknown> = {
        schema: "pushrun.provider/v1",
        id: id.trim(),
        name: name.trim(),
        ...(description.trim() ? { description: description.trim() } : {}),
        ...(hasWarmup ? { warmup: warmupPath } : {}),
        install: installPath,
        parameters: parameters.map((parameter) => {
          const next: Param = {
            ...parameter,
            id: parameter.id.trim(),
            label: parameter.label?.trim(),
            options:
              parameter.type === "select" && parameter.options
                ? parameter.options.map((option) => option.trim()).filter(Boolean)
                : undefined,
          };
          if (!next.label) delete next.label;
          if (!next.options?.length) delete next.options;
          if (next.default === "") delete next.default;
          return next;
        }),
        ...(Object.keys(scripts).length > 0 ? { scripts } : {}),
      };
      return apiFetch<Provider>(
        isCreate ? "/providers" : `/providers/${encodeURIComponent(provider.id)}`,
        {
          method: isCreate ? "POST" : "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
    },
    onSuccess: (saved) => {
      toast(
        t(isCreate ? "providers.editor.created" : "providers.editor.saved", {
          id: saved.id || id.trim(),
        }),
      );
      void queryClient.invalidateQueries({ queryKey: ["providers"] });
      onClose();
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === "conflict") {
        setError(t("providers.editor.conflict"));
        return;
      }
      const message = err instanceof ApiError ? `${err.code}: ${err.message}` : String(err);
      setError(t("providers.editor.failed", { message }));
    },
  });

  const handleSubmit = () => {
    const validationError = validate();
    if (validationError) {
      setError(validationError);
      return;
    }
    setError(null);
    saveMutation.mutate();
  };

  return (
    <>
      {!confirmDiscard && (
        <Dialog
          open
          title={
            isCreate
              ? t("providers.editor.createTitle")
              : t("providers.editor.editTitle", { id: provider.id })
          }
          fullscreen
          onClose={requestClose}
          closeLabel={t("common.close")}
          footer={
            <>
              <Button variant="ghost" onClick={requestClose}>
                {t("common.cancel")}
              </Button>
              <Button disabled={saveMutation.isPending} onClick={handleSubmit}>
                {saveMutation.isPending
                  ? t("providers.editor.saving")
                  : isCreate
                    ? t("providers.editor.create")
                    : t("providers.editor.save")}
              </Button>
            </>
          }
        >
          <div className="grid items-start gap-6 lg:grid-cols-[minmax(340px,420px)_minmax(0,1fr)]">
            <div className="space-y-6">
            <section className="panel space-y-3 p-4">
              <div>
                <h3 className="text-[15px] font-semibold">
                  {t("providers.editor.identityTitle")}
                </h3>
                <p className="mt-1 text-[13px] leading-5 text-muted-foreground">
                  {t("providers.editor.identityDescription")}
                </p>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor={`${fieldPrefix}-id`}>{t("providers.editor.id")}</Label>
                  <Input
                    id={`${fieldPrefix}-id`}
                    value={id}
                    placeholder={t("providers.editor.idPlaceholder")}
                    disabled={!isCreate}
                    className="font-mono"
                    onChange={(e) => setId(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor={`${fieldPrefix}-name`}>{t("providers.editor.name")}</Label>
                  <Input
                    id={`${fieldPrefix}-name`}
                    value={name}
                    placeholder={t("providers.editor.namePlaceholder")}
                    onChange={(e) => setName(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5 sm:col-span-2">
                  <Label htmlFor={`${fieldPrefix}-description`}>
                    {t("providers.editor.description")}
                  </Label>
                  <Textarea
                    id={`${fieldPrefix}-description`}
                    value={description}
                    rows={2}
                    onChange={(e) => setDescription(e.target.value)}
                  />
                </div>
              </div>
            </section>

            <section className="panel space-y-3 p-4">
              <div>
                <h3 className="text-[15px] font-semibold">
                  {t("providers.editor.parametersTitle")}
                </h3>
                <p className="mt-1 text-[13px] leading-5 text-muted-foreground">
                  {t("providers.editor.parametersDescription")}
                </p>
              </div>
              <div className="space-y-2">
                {parameters.map((parameter, index) => (
                  <div key={index} className="rounded-md border bg-secondary/40 p-3">
                    <div className="flex items-center gap-2">
                      <span className="text-[12.5px] font-semibold text-muted-foreground">
                        {t("providers.editor.parameterIndex", { index: index + 1 })}
                      </span>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="ml-auto"
                        aria-label={t("providers.editor.removeParameter", { index: index + 1 })}
                        onClick={() => removeParam(index)}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                    <div className="mt-2 grid gap-3 sm:grid-cols-2">
                      <div className="space-y-1">
                        <Label className="text-[11.5px] font-medium text-muted-foreground">
                          {t("providers.editor.paramID")}
                        </Label>
                        <Input
                          value={parameter.id}
                          className="font-mono"
                          aria-label={`${t("providers.editor.paramID")} ${index + 1}`}
                          onChange={(e) => updateParam(index, { id: e.target.value })}
                        />
                      </div>
                      <div className="space-y-1">
                        <Label className="text-[11.5px] font-medium text-muted-foreground">
                          {t("providers.editor.paramLabel")}
                        </Label>
                        <Input
                          value={parameter.label ?? ""}
                          aria-label={`${t("providers.editor.paramLabel")} ${index + 1}`}
                          onChange={(e) => updateParam(index, { label: e.target.value })}
                        />
                      </div>
                      <div className="space-y-1">
                        <Label className="text-[11.5px] font-medium text-muted-foreground">
                          {t("providers.editor.paramType")}
                        </Label>
                        <Select
                          value={parameter.type}
                          aria-label={`${t("providers.editor.paramType")} ${index + 1}`}
                          onChange={(e) =>
                            updateParam(index, { type: e.target.value as Param["type"] })
                          }
                        >
                          <option value="string">string</option>
                          <option value="number">number</option>
                          <option value="boolean">boolean</option>
                          <option value="select">select</option>
                        </Select>
                      </div>
                      <div className="space-y-1">
                        <Label className="text-[11.5px] font-medium text-muted-foreground">
                          {t("providers.editor.paramScope")}
                        </Label>
                        <Select
                          value={parameter.scope}
                          aria-label={`${t("providers.editor.paramScope")} ${index + 1}`}
                          onChange={(e) =>
                            updateParam(index, { scope: e.target.value as Param["scope"] })
                          }
                        >
                          <option value="warmup">{t("params.scope.warmup")}</option>
                          <option value="install">{t("params.scope.install")}</option>
                        </Select>
                      </div>
                    </div>
                    <div className="mt-3 grid gap-3 sm:grid-cols-2">
                      <div className="space-y-1 sm:col-span-2">
                        <Label className="text-[11.5px] font-medium text-muted-foreground">
                          {t("providers.editor.paramDefault")}
                        </Label>
                        <Input
                          value={parameter.default ?? ""}
                          aria-label={`${t("providers.editor.paramDefault")} ${index + 1}`}
                          onChange={(e) => updateParam(index, { default: e.target.value })}
                        />
                      </div>
                      {parameter.type === "select" && (
                        <div className="space-y-1 sm:col-span-2">
                          <Label className="text-[11.5px] font-medium text-muted-foreground">
                            {t("providers.editor.paramOptions")}
                          </Label>
                          <Input
                            value={(parameter.options ?? []).join(", ")}
                            placeholder="a, b, c"
                            aria-label={`${t("providers.editor.paramOptions")} ${index + 1}`}
                            onChange={(e) =>
                              updateParam(index, {
                                options: e.target.value.split(",").map((option) => option.trim()),
                              })
                            }
                          />
                        </div>
                      )}
                      <div className="flex flex-wrap items-center gap-4 pt-5">
                        <span className="flex items-center gap-2">
                          <Switch
                            id={`param-required-${index}`}
                            checked={parameter.required === true}
                            onCheckedChange={(checked) =>
                              updateParam(index, { required: checked || undefined })
                            }
                          />
                          <Label htmlFor={`param-required-${index}`} className="text-[13px]">
                            {t("providers.card.required")}
                          </Label>
                        </span>
                        <span className="flex items-center gap-2">
                          <Switch
                            id={`param-secret-${index}`}
                            checked={parameter.secret === true}
                            onCheckedChange={(checked) =>
                              updateParam(index, { secret: checked || undefined })
                            }
                          />
                          <Label htmlFor={`param-secret-${index}`} className="text-[13px]">
                            {t("providers.card.secret")}
                          </Label>
                        </span>
                      </div>
                    </div>
                  </div>
                ))}
                <Button size="sm" variant="outline" onClick={addParam}>
                  <Plus className="h-4 w-4" aria-hidden="true" />
                  {t("providers.editor.addParameter")}
                </Button>
              </div>
            </section>

            </div>

            <div className="space-y-6">
            <section className="panel space-y-4 p-4">
              <div>
                <h3 className="text-[15px] font-semibold">
                  {t("providers.editor.scriptsTitle")}
                </h3>
                <p className="mt-1 text-[13px] leading-5 text-muted-foreground">
                  {t("providers.editor.scriptsDescription")}
                </p>
              </div>
              <div className="space-y-1.5">
                <Label>{t("providers.editor.warmupScript")}</Label>
                <ScriptEditor
                  aria-label={t("providers.editor.warmupScript")}
                  value={warmupScript}
                  placeholder="#!/usr/bin/env bash&#10;set -euo pipefail"
                  onChange={setWarmupScript}
                />
                <p className="text-[13px] leading-5 text-muted-foreground">
                  {t("providers.editor.warmupHint")}
                </p>
              </div>
              <div className="space-y-1.5">
                <Label>{t("providers.editor.installScript")}</Label>
                <ScriptEditor
                  aria-label={t("providers.editor.installScript")}
                  value={installScript}
                  placeholder="#!/usr/bin/env bash&#10;set -euo pipefail"
                  onChange={setInstallScript}
                />
                <p className="text-[13px] leading-5 text-muted-foreground">
                  {t("providers.editor.installHint")}
                </p>
              </div>
              {!isCreate && (
                <p className="text-[13px] leading-5 text-muted-foreground">
                  {t("providers.editor.scriptKeepHint")}
                </p>
              )}
            </section>

            {error && (
              <p
                role="alert"
                className="rounded-md border border-danger-border bg-danger-bg px-3 py-2 text-sm whitespace-pre-wrap text-destructive"
              >
                {error}
              </p>
            )}
            </div>
          </div>
        </Dialog>
      )}

      {confirmDiscard && (
        <Dialog
          open
          title={t("providers.editor.discardTitle")}
          onClose={cancelDiscard}
          closeLabel={t("common.close")}
          footer={
            <>
              <Button variant="ghost" onClick={cancelDiscard}>
                {t("common.cancel")}
              </Button>
              <Button variant="destructive" onClick={discard}>
                {t("providers.editor.discard")}
              </Button>
            </>
          }
        >
          {t("providers.editor.discardBody")}
        </Dialog>
      )}
    </>
  );
}
