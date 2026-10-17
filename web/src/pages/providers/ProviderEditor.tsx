import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, apiFetch } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { Provider } from "@/lib/types";

export interface ProviderEditorProps {
  // null means create; the parent mounts this dialog only while it is open,
  // so the initial state always comes from a fresh mount.
  provider: Provider | null;
  onClose: () => void;
}

// The editor carries the descriptor plus script contents. On edit the
// textareas preload from the single-provider GET (the list payload has no
// scripts), so the form mounts only once those contents are known. A
// non-empty textarea is always sent; an empty one keeps the server content
// only when the server had none — clearing a preloaded script sends the
// empty string to overwrite it.
export default function ProviderEditor({ provider, onClose }: ProviderEditorProps) {
  const { t } = useTranslation();
  const isCreate = provider === null;

  // The list payload carries no script contents, so on edit fetch the
  // single-provider view unless the caller already supplied scripts.
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
      <Dialog open title={t("providers.editor.editTitle", { id: provider.id })}>
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

  const [id, setId] = useState(provider?.id ?? "");
  const [name, setName] = useState(provider?.name ?? "");
  const [description, setDescription] = useState(provider?.description ?? "");
  const [warmupPath, setWarmupPath] = useState(provider?.warmup ?? "");
  const [installPath, setInstallPath] = useState(provider?.install ?? "scripts/install.sh");
  const [warmupScript, setWarmupScript] = useState(serverScripts?.[provider?.warmup ?? ""] ?? "");
  const [installScript, setInstallScript] = useState(
    serverScripts?.[provider?.install ?? ""] ?? "",
  );
  const [error, setError] = useState<string | null>(null);

  const saveMutation = useMutation({
    mutationFn: () => {
      const scripts: Record<string, string> = {};
      const addScript = (path: string, content: string) => {
        const p = path.trim();
        if (p === "") return;
        // An empty textarea keeps the server content only when the server
        // had none; clearing a preloaded script sends "" to overwrite it.
        if (content !== "" || (serverScripts !== null && serverScripts[p] !== undefined)) {
          scripts[p] = content;
        }
      };
      addScript(warmupPath, warmupScript);
      addScript(installPath, installScript);
      const body: Record<string, unknown> = {
        schema: "pushrun.provider/v1",
        id: id.trim(),
        name: name.trim(),
        ...(description.trim() ? { description: description.trim() } : {}),
        ...(warmupPath.trim() ? { warmup: warmupPath.trim() } : {}),
        install: installPath.trim(),
        // Declared parameters are not edited here; keep them verbatim.
        ...(!isCreate && provider.parameters?.length
          ? { parameters: provider.parameters }
          : {}),
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
    // A script without its path cannot be persisted; on create a declared
    // warmup path also needs its content (there is nothing server-side yet).
    const warmupMismatch =
      (warmupScript !== "" && warmupPath.trim() === "") ||
      (isCreate && warmupPath.trim() !== "" && warmupScript === "");
    if (
      id.trim() === "" ||
      name.trim() === "" ||
      installPath.trim() === "" ||
      (isCreate && installScript === "") ||
      warmupMismatch
    ) {
      setError(t("providers.editor.missing"));
      return;
    }
    setError(null);
    saveMutation.mutate();
  };

  return (
    <Dialog
      open
      title={
        isCreate
          ? t("providers.editor.createTitle")
          : t("providers.editor.editTitle", { id: provider.id })
      }
      className="max-h-[85vh] max-w-lg overflow-y-auto"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
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
      <div className="space-y-3">
        <div className="space-y-1">
          <Label htmlFor={`${fieldPrefix}-id`}>{t("providers.editor.id")}</Label>
          <Input
            id={`${fieldPrefix}-id`}
            value={id}
            placeholder={t("providers.editor.idPlaceholder")}
            disabled={!isCreate}
            onChange={(e) => setId(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${fieldPrefix}-name`}>{t("providers.editor.name")}</Label>
          <Input
            id={`${fieldPrefix}-name`}
            value={name}
            placeholder={t("providers.editor.namePlaceholder")}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${fieldPrefix}-description`}>
            {t("providers.editor.description")}
          </Label>
          <Input
            id={`${fieldPrefix}-description`}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <Label htmlFor={`${fieldPrefix}-warmup-path`}>
              {t("providers.editor.warmupPath")}
            </Label>
            <Input
              id={`${fieldPrefix}-warmup-path`}
              value={warmupPath}
              placeholder="scripts/warmup.sh"
              onChange={(e) => setWarmupPath(e.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor={`${fieldPrefix}-install-path`}>
              {t("providers.editor.installPath")}
            </Label>
            <Input
              id={`${fieldPrefix}-install-path`}
              value={installPath}
              placeholder="scripts/install.sh"
              onChange={(e) => setInstallPath(e.target.value)}
            />
          </div>
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${fieldPrefix}-warmup-script`}>
            {t("providers.editor.warmupScript")}
          </Label>
          <Textarea
            id={`${fieldPrefix}-warmup-script`}
            className="min-h-24 font-mono text-xs"
            value={warmupScript}
            spellCheck={false}
            onChange={(e) => setWarmupScript(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${fieldPrefix}-install-script`}>
            {t("providers.editor.installScript")}
          </Label>
          <Textarea
            id={`${fieldPrefix}-install-script`}
            className="min-h-24 font-mono text-xs"
            value={installScript}
            spellCheck={false}
            onChange={(e) => setInstallScript(e.target.value)}
          />
        </div>
        {!isCreate && (
          <p className="text-xs text-muted-foreground">
            {t("providers.editor.scriptKeepHint")}
          </p>
        )}
        {error && (
          <p role="alert" className="text-sm whitespace-pre-wrap text-destructive">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}
