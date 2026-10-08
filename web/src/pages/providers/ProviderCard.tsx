import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import ParamForm from "@/components/ParamForm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { API_BASE, ApiError, apiFetch, parseErrorBody } from "@/lib/api";
import { tokenStore } from "@/lib/auth";
import { missingRequiredParams } from "@/lib/params";
import { toast } from "@/lib/toast";
import type { Provider } from "@/lib/types";

interface WarmupResponse {
  provider: string;
  status: string;
  error?: string;
}

type LifecycleStatus = "cold" | "warming" | "warm";

// Card status aggregates the shared-artifact cache entries: an in-flight
// entry wins, otherwise any entry means warm. A failed warmup is not a
// persisted state — it arrives on the warmup POST response.
function lifecycleStatus(provider: Provider): LifecycleStatus {
  const warmups = provider.warmups ?? [];
  if (warmups.some((w) => w.warming)) return "warming";
  if (warmups.length > 0) return "warm";
  return "cold";
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

export interface ProviderCardProps {
  provider: Provider;
  onEdit: (provider: Provider) => void;
}

export default function ProviderCard({ provider, onEdit }: ProviderCardProps) {
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
    <section
      data-testid={`provider-card-${provider.id}`}
      className="space-y-4 rounded-lg border p-4"
    >
      <div className="flex flex-wrap items-start gap-2">
        <div>
          <h2 className="text-lg font-semibold">{provider.name}</h2>
          <div className="font-mono text-xs text-muted-foreground">{provider.id}</div>
        </div>
        <Badge
          variant={status === "warm" ? "success" : status === "warming" ? "running" : "muted"}
        >
          {t(`providers.card.status.${status}`)}
        </Badge>
        {status === "warm" && latest && (
          <span className="text-xs text-muted-foreground">
            {t("providers.card.warmedAt", { time: formatTime(latest) })}
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
      {provider.description && (
        <p className="text-sm text-muted-foreground">{provider.description}</p>
      )}
      {referencedBy && (
        <p role="alert" className="text-sm text-destructive">
          {t("providers.card.referencedBy", { projects: referencedBy.join(", ") })}
        </p>
      )}
      <dl className="grid gap-2 sm:grid-cols-2">
        <div>
          <dt className="text-xs text-muted-foreground">{t("params.scope.warmup")}</dt>
          <dd className="font-mono text-xs">{provider.warmup || "—"}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">{t("params.scope.install")}</dt>
          <dd className="font-mono text-xs">{provider.install}</dd>
        </div>
      </dl>
      {warmups.length > 0 && (
        <ul className="space-y-1 text-xs">
          {warmups.map((w) => (
            <li key={w.fingerprint} className="flex flex-wrap items-center gap-2">
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
          <ul className="mt-1 space-y-1 text-sm">
            {declaredParams.map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-2">
                <span>{p.label ?? p.id}</span>
                <Badge variant="muted">{t(`params.scope.${p.scope}`)}</Badge>
                {p.required && <Badge variant="outline">{t("providers.card.required")}</Badge>}
                {p.secret && <Badge variant="outline">{t("providers.card.secret")}</Badge>}
              </li>
            ))}
          </ul>
        )}
      </div>
      {provider.warmup && (
        <div className="space-y-2 border-t pt-3">
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
