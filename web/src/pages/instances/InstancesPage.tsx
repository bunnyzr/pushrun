import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiFetch } from "@/lib/api";
import type { InstanceState } from "@/lib/types";
import InstanceDetail from "./InstanceDetail";
import InstanceList from "./InstanceList";

export default function InstancesPage() {
  const { t } = useTranslation();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const query = useQuery({
    queryKey: ["instances"],
    queryFn: ({ signal }) => apiFetch<{ instances: InstanceState[] }>("/instances", { signal }),
    refetchInterval: 5000,
  });

  const instances = query.data?.instances ?? [];
  const selected =
    instances.find((i) => `${i.project}/${i.instance}` === selectedId) ?? null;

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-bold">{t("pages.instances")}</h1>
      {query.isError && <p className="text-sm text-destructive">{t("instances.loadError")}</p>}
      {query.isLoading ? (
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      ) : instances.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("instances.empty")}</p>
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
          <InstanceList instances={instances} selectedId={selectedId} onSelect={setSelectedId} />
          {selected ? (
            <InstanceDetail
              key={`${selected.project}/${selected.instance}`}
              instance={selected}
              onDeleted={() => setSelectedId(null)}
            />
          ) : (
            <div className="rounded-lg border p-6 text-sm text-muted-foreground">
              {t("instances.selectHint")}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
