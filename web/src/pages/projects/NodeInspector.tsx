import { useTranslation } from "react-i18next";
import ParamForm from "@/components/ParamForm";
import { Badge, type BadgeProps } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import type { Mount, MountStatus, Provider, TreeNode } from "@/lib/types";

const STATUS_VARIANTS: Record<MountStatus["status"], BadgeProps["variant"]> = {
  cold: "muted",
  warming: "running",
  warm: "success",
};

export interface NodeInspectorProps {
  node: TreeNode;
  providers: Provider[];
  status?: MountStatus;
  showErrors: boolean;
  onChange: (node: TreeNode) => void;
}

export default function NodeInspector({
  node,
  providers,
  status,
  showErrors,
  onChange,
}: NodeInspectorProps) {
  const { t } = useTranslation();
  const mount = node.mount;
  const provider = providers.find((p) => p.id === mount?.provider);

  const setMount = (next: Mount | undefined) => {
    onChange(next ? { path: node.path, mount: next } : { path: node.path });
  };

  const toggleMount = (on: boolean) => {
    if (!on) {
      setMount(undefined);
      return;
    }
    const first = providers[0];
    const params: Record<string, string> = {};
    for (const param of first?.parameters ?? []) {
      if (param.default !== undefined) params[param.id] = param.default;
    }
    setMount({ provider: first?.id ?? "", params });
  };

  const changeProvider = (id: string) => {
    if (!mount) return;
    const next = providers.find((p) => p.id === id);
    const params: Record<string, string> = {};
    for (const param of next?.parameters ?? []) {
      params[param.id] = mount.params?.[param.id] ?? param.default ?? "";
    }
    setMount({ ...mount, provider: id, params });
  };

  return (
    <section aria-label={t("projects.inspector.title")} className="rounded-lg border p-4">
      <h3 className="mb-3 text-sm font-semibold">{t("projects.inspector.title")}</h3>
      <div className="space-y-4">
        <div className="space-y-1">
          <Label>{t("projects.inspector.path")}</Label>
          <p className="font-mono text-sm break-all">{node.path}</p>
        </div>
        <div className="flex items-center gap-2">
          <Switch id="inspector-mount" checked={mount !== undefined} onCheckedChange={toggleMount} />
          <Label htmlFor="inspector-mount">{t("projects.inspector.mount")}</Label>
        </div>
        {mount && (
          <>
            <div className="space-y-1">
              <Label htmlFor="inspector-provider">{t("projects.inspector.provider")}</Label>
              <Select
                id="inspector-provider"
                value={mount.provider}
                onChange={(e) => changeProvider(e.target.value)}
              >
                {mount.provider === "" && (
                  <option value="">{t("projects.inspector.noProvider")}</option>
                )}
                {providers.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} ({p.id})
                  </option>
                ))}
              </Select>
            </div>
            <div className="flex items-center gap-2">
              <Switch
                id="inspector-primary"
                checked={mount.primary === true}
                onCheckedChange={(v) => setMount({ ...mount, primary: v || undefined })}
              />
              <Label htmlFor="inspector-primary">{t("projects.inspector.primary")}</Label>
            </div>
            {status && (
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Badge variant={STATUS_VARIANTS[status.status]}>
                  {t(`projects.warmupStatus.${status.status}`)}
                </Badge>
                {status.warmed_at && <span>{new Date(status.warmed_at).toLocaleString()}</span>}
              </div>
            )}
            {(provider?.parameters ?? []).length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("projects.inspector.noParams")}</p>
            ) : (
              <ParamForm
                params={provider?.parameters ?? []}
                values={mount.params ?? {}}
                showErrors={showErrors}
                idPrefix={`params-${node.path}`}
                onChange={(values) => setMount({ ...mount, params: values })}
              />
            )}
          </>
        )}
      </div>
    </section>
  );
}
