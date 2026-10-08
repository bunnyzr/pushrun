import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { ApiError, apiFetch } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { Provider } from "@/lib/types";
import ProviderCard from "./ProviderCard";
import ProviderEditor from "./ProviderEditor";

export default function ProvidersPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const fileRef = useRef<HTMLInputElement>(null);
  // null = dialog closed; {provider: null} = create mode.
  const [editor, setEditor] = useState<{ provider: Provider | null } | null>(null);
  const [importError, setImportError] = useState<string | null>(null);

  const providersQuery = useQuery({
    queryKey: ["providers"],
    queryFn: ({ signal }) => apiFetch<{ providers: Provider[] }>("/providers", { signal }),
    refetchInterval: (query) =>
      (query.state.data?.providers ?? []).some((p) => (p.warmups ?? []).some((w) => w.warming))
        ? 2000
        : false,
  });

  // The builtins (git, symlink) are platform-owned and never shown here.
  const external = (providersQuery.data?.providers ?? []).filter((p) => !p.builtin);

  const importMutation = useMutation({
    mutationFn: (file: File) =>
      apiFetch<{ status: string; provider: Provider }>("/providers/import", {
        method: "POST",
        headers: { "Content-Type": "application/gzip" },
        body: file,
      }),
    onSuccess: (res) => {
      setImportError(null);
      toast(t("providers.imported", { id: res.provider.id }));
      void queryClient.invalidateQueries({ queryKey: ["providers"] });
    },
    onError: (err) => {
      const message = err instanceof ApiError ? `${err.code}: ${err.message}` : String(err);
      setImportError(t("providers.importFailed", { message }));
    },
  });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-2xl font-bold">{t("pages.providers")}</h1>
        <div className="ml-auto flex gap-2">
          <Button size="sm" onClick={() => setEditor({ provider: null })}>
            {t("providers.new")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={importMutation.isPending}
            onClick={() => fileRef.current?.click()}
          >
            {t("providers.import")}
          </Button>
          <input
            ref={fileRef}
            type="file"
            accept=".tar.gz,.tgz,application/gzip"
            aria-label={t("providers.importLabel")}
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              e.target.value = "";
              if (file) importMutation.mutate(file);
            }}
          />
        </div>
      </div>
      {importError && (
        <p role="alert" className="text-sm text-destructive">
          {importError}
        </p>
      )}
      {providersQuery.isLoading ? (
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      ) : providersQuery.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {t("providers.loadError")}
        </p>
      ) : external.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("providers.empty")}</p>
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-2">
          {external.map((provider) => (
            <ProviderCard
              key={provider.id}
              provider={provider}
              onEdit={(p) => setEditor({ provider: p })}
            />
          ))}
        </div>
      )}
      {editor && (
        <ProviderEditor provider={editor.provider} onClose={() => setEditor(null)} />
      )}
    </div>
  );
}
