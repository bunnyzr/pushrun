import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import KV from "@/components/KV";
import ParamForm from "@/components/ParamForm";
import RelativeTime from "@/components/RelativeTime";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { API_BASE, ApiError, apiFetch, parseErrorBody } from "@/lib/api";
import { tokenStore } from "@/lib/auth";
import { missingRequiredParams } from "@/lib/params";
import { toast } from "@/lib/toast";
import type { Provider } from "@/lib/types";
import LifecycleBadge from "./LifecycleBadge";
import { lifecycleStatus } from "./lifecycle";

interface WarmupResponse {
  provider: string;
  status: string;
  error?: string;
}

function formatTime(ts: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString();
}

function detail(err: unknown): string {
  return err instanceof ApiError ? `${err.code}: ${err.message}` : String(err);
}

// Export is a binary download, so it bypasses apiFetch (which parses JSON).
// The token travels in the Authorization header, never in the URL.
async function downloadProvider(id: string): Promise<void> {
  const headers = new Headers();
  const token = tokenStore.get();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(`${API_BASE}/providers/${encodeURIComponent(id)}/export`, { headers });
  if (!res.ok) throw await parseErrorBody(res);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = `${id}.tar.gz`;
    a.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}

export interface ProviderDetailProps {
  provider: Provider;
  onEdit: (provider: Provider) => void;
  onDeleted: () => void;
}

export default function ProviderDetail({ provider, onEdit, onDeleted }: ProviderDetailProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [referencedBy, setReferencedBy] = useState<string[] | null>(null);
  const [warmError, setWarmError] = useState<string | null>(null);
  const [showWarmErrors, setShowWarmErrors] = useState(false);
  const [warmValues, setWarmValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      (provider.parameters ?? [])
        .filter((p) => p.scope === "warmup" && p.default !== undefined)
        .map((p) => [p.id, p.default as string]),
    ),
  );

  const status = lifecycleStatus(provider);
  const warmups = provider.warmups ?? [];
  const latest = warmups
    .filter((w) => !w.warming)
    .map((w) => w.warmed_at)
    .sort()
    .at(-1);
  const warmupParams = (provider.parameters ?? []).filter((p) => p.scope === "warmup");
  const declaredParams = provider.parameters ?? [];

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ["providers"] });

  const warmupMutation = useMutation({
    mutationFn: () =>
      apiFetch<WarmupResponse>(`/providers/${encodeURIComponent(provider.id)}/warmup`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ params: warmValues }),
      }),
    onSuccess: (res) => {
      invalidate();
      // Warmup failure is reported on a 200 response, not as an HTTP error.
      if (res.status === "FAILED") {
        const message = res.error ?? res.status;
        setWarmError(t("providers.card.warmupFailed", { message }));
        toast(t("providers.card.warmupFailed", { message }), "error");
        return;
      }
      setWarmError(null);
      toast(t("providers.card.warmupSuccess", { status: res.status }));
    },
    onError: (err) => {
      const message = detail(err);
      setWarmError(t("providers.card.warmupFailed", { message }));
      toast(t("providers.card.warmupFailed", { message }), "error");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>(`/providers/${encodeURIComponent(provider.id)}`, {
        method: "DELETE",
      }),
    onSuccess: () => {
      toast(t("providers.card.deleted", { id: provider.id }));
      invalidate();
      onDeleted();
    },
    onError: (err) => {
      setConfirmDelete(false);
      if (err instanceof ApiError && Array.isArray(err.extra?.referenced_by)) {
        setReferencedBy(err.extra.referenced_by as string[]);
        return;
      }
      toast(t("providers.card.deleteFailed", { message: detail(err) }), "error");
    },
  });

  const handleWarmup = () => {
    const missing = missingRequiredParams(warmupParams, warmValues);
    if (missing.length > 0) {
      setShowWarmErrors(true);
      setWarmError(t("providers.card.warmupBlocked", { fields: missing.join(", ") }));
      return;
    }
    setShowWarmErrors(false);
    setWarmError(null);
    warmupMutation.mutate();
  };

  const handleExport = () => {
    downloadProvider(provider.id).catch((err: unknown) => {
      toast(t("providers.card.exportFailed", { message: detail(err) }), "error");
    });
  };

  return (
    <section data-testid={`provider-card-${provider.id}`} className="space-y-5">
      <div>
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-xl font-bold tracking-tight">{provider.name}</h2>
          <LifecycleBadge status={status} />
          {status === "warm" && latest && (
            <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
              {t("providers.card.warmed")}
              <RelativeTime ts={latest} />
            </span>
          )}
          <div className="ml-auto flex flex-wrap gap-2">
            <Button size="sm" variant="outline" onClick={() => onEdit(provider)}>
              {t("providers.card.edit")}
            </Button>
            <Button size="sm" variant="outline" onClick={handleExport}>
              {t("providers.card.export")}
            </Button>
            {confirmDelete ? (
              <>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={deleteMutation.isPending}
                  onClick={() => deleteMutation.mutate()}
                >
                  {t("providers.card.confirmDelete")}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
                  {t("common.cancel")}
                </Button>
              </>
            ) : (
              <Button
                size="sm"
                variant="destructive"
                onClick={() => {
                  setReferencedBy(null);
                  setConfirmDelete(true);
                }}
              >
                {t("providers.card.delete")}
              </Button>
            )}
          </div>
        </div>
        <div className="mt-1 font-mono text-xs text-muted-foreground">{provider.id}</div>
      </div>

      {provider.description && (
        <p className="text-sm text-muted-foreground">{provider.description}</p>
      )}
      {referencedBy && (
        <p role="alert" className="text-sm text-destructive">
          {t("providers.card.referencedBy", { projects: referencedBy.join(", ") })}
        </p>
      )}

      <div className="grid grid-cols-2 gap-x-6 gap-y-3 rounded-lg border bg-card p-4">
        <KV label={t("params.scope.warmup")}>
          {provider.warmup ? <span className="font-mono">{provider.warmup}</span> : undefined}
        </KV>
        <KV label={t("params.scope.install")}>
          {provider.install ? <span className="font-mono">{provider.install}</span> : undefined}
        </KV>
      </div>

      {warmups.length > 0 && (
        <ul className="space-y-1 text-xs">
          {warmups.map((w) => (
            <li
              key={w.fingerprint}
              className="flex flex-wrap items-center gap-2 rounded-md border bg-card px-3 py-1.5"
            >
              <span className="font-mono">{w.fingerprint.slice(0, 12)}</span>
              {w.warming ? (
                <Badge variant="running">{t("providers.card.warmingEntry")}</Badge>
              ) : (
                <span className="text-muted-foreground">{formatTime(w.warmed_at)}</span>
              )}
              {w.params && (
                <span className="font-mono text-muted-foreground">
                  {Object.entries(w.params)
                    .map(([k, v]) => `${k}=${v}`)
                    .join(", ")}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}

      <div>
        <h3 className="text-xs font-medium text-muted-foreground">
          {t("providers.card.paramsTitle")}
        </h3>
        {declaredParams.length === 0 ? (
          <p className="mt-1 text-xs text-muted-foreground">{t("providers.card.noParams")}</p>
        ) : (
          <ul className="mt-2 divide-y rounded-lg border bg-card">
            {declaredParams.map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-2 px-3 py-2 text-sm">
                <span className="font-medium">{p.label ?? p.id}</span>
                <Badge variant="muted">{t(`params.scope.${p.scope}`)}</Badge>
                {p.required && <Badge variant="outline">{t("providers.card.required")}</Badge>}
                {p.secret && <Badge variant="outline">{t("providers.card.secret")}</Badge>}
                <span className="ml-auto font-mono text-xs text-muted-foreground">{p.id}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      {provider.warmup && (
        <div className="space-y-3 rounded-lg border bg-card p-4">
          {warmupParams.length > 0 && (
            <>
              <h3 className="text-xs font-medium text-muted-foreground">
                {t("providers.card.warmupParams")}
              </h3>
              <ParamForm
                params={warmupParams}
                values={warmValues}
                onChange={setWarmValues}
                showErrors={showWarmErrors}
                idPrefix={`warm-${provider.id}`}
              />
            </>
          )}
          <Button size="sm" disabled={warmupMutation.isPending} onClick={handleWarmup}>
            {warmupMutation.isPending
              ? t("providers.card.warming")
              : status === "warm"
                ? t("providers.card.rewarm")
                : t("providers.card.warmup")}
          </Button>
          {warmError && (
            <p role="alert" className="text-sm whitespace-pre-wrap text-destructive">
              {warmError}
            </p>
          )}
        </div>
      )}
    </section>
  );
}
