import { Command } from "cmdk";
import { Boxes, FolderGit2, Globe, Layers, Play, RotateCcw, Square, Zap } from "lucide-react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLanguage } from "@/i18n";
import { ApiError, apiFetch } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { InstanceState, Project, Provider } from "@/lib/types";

// CommandPalette is the console's keyboard-first control surface: ⌘K opens
// it, fuzzy search jumps to any object, and instance/project actions run
// right from the list without navigating.
export default function CommandPalette({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [lang, setLang] = useLanguage();

  const instancesQuery = useQuery({
    queryKey: ["instances"],
    queryFn: ({ signal }) => apiFetch<{ instances: InstanceState[] }>("/instances", { signal }),
    enabled: open,
  });
  const projectsQuery = useQuery({
    queryKey: ["projects"],
    queryFn: ({ signal }) => apiFetch<{ projects: Project[] }>("/projects", { signal }),
    enabled: open,
  });
  const providersQuery = useQuery({
    queryKey: ["providers"],
    queryFn: ({ signal }) => apiFetch<{ providers: Provider[] }>("/providers", { signal }),
    enabled: open,
  });

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const instances = instancesQuery.data?.instances ?? [];
  const projects = projectsQuery.data?.projects ?? [];
  const providers = (providersQuery.data?.providers ?? []).filter((p) => !p.builtin);

  const go = (path: string) => {
    onClose();
    void navigate(path);
  };

  const instanceAction = (inst: InstanceState, action: "run" | "stop" | "restart" | "rerun") => {
    onClose();
    apiFetch(`/instances/${inst.project}/${inst.instance}/${action}`, { method: "POST" })
      .then(() => {
        toast(t("instances.toast.actionQueued", { action: t(`instances.actions.${action}`) }));
        void queryClient.invalidateQueries({ queryKey: ["instances"] });
      })
      .catch((err: unknown) => {
        if (err instanceof ApiError && err.code === "busy") {
          toast(t("instances.toast.busy"), "error");
        } else {
          toast(err instanceof Error ? err.message : String(err), "error");
        }
      });
  };

  const warmupProject = (name: string) => {
    onClose();
    apiFetch(`/projects/${encodeURIComponent(name)}/warmup`, { method: "POST" })
      .then(() => toast(t("palette.warmupQueued", { name })))
      .catch((err: unknown) => toast(err instanceof Error ? err.message : String(err), "error"));
  };

  const item =
    "flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-2 text-sm aria-selected:bg-accent aria-selected:text-accent-foreground";

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-[18vh]"
      onClick={onClose}
    >
      <Command
        label={t("palette.title")}
        className="w-[560px] max-w-[92vw] overflow-hidden rounded-lg border bg-popover shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <Command.Input
          autoFocus
          placeholder={t("palette.placeholder")}
          className="w-full border-b bg-transparent px-4 py-3 text-sm outline-none placeholder:text-muted-foreground"
        />
        <Command.List className="max-h-[50vh] overflow-y-auto p-2">
          <Command.Empty className="px-3 py-6 text-center text-sm text-muted-foreground">
            {t("palette.noResults")}
          </Command.Empty>

          {instances.length > 0 && (
            <Command.Group heading={t("palette.instances")} className="mb-1">
              {instances.map((inst) => {
                const id = `${inst.project}/${inst.instance}`;
                return (
                  <Command.Item
                    key={id}
                    value={`instance ${id} ${inst.branch ?? ""}`}
                    keywords={[inst.project, inst.instance, inst.branch ?? ""]}
                    onSelect={() => go(`/instances?sel=${encodeURIComponent(id)}`)}
                    className={item}
                  >
                    <Boxes size={14} className="flex-none text-muted-foreground" aria-hidden />
                    <span className="flex-1 truncate">{id}</span>
                    <span className="text-xs text-muted-foreground">{t("palette.open")}</span>
                  </Command.Item>
                );
              })}
            </Command.Group>
          )}

          {projects.length > 0 && (
            <Command.Group heading={t("palette.projects")} className="mb-1">
              {projects.map((p) => (
                <Command.Item
                  key={p.name}
                  value={`project ${p.name} ${p.display_name ?? ""}`}
                  keywords={[p.name, p.display_name ?? ""]}
                  onSelect={() => go(`/projects?sel=${encodeURIComponent(p.name)}`)}
                  className={item}
                >
                  <FolderGit2 size={14} className="flex-none text-muted-foreground" aria-hidden />
                  <span className="flex-1 truncate">{p.display_name || p.name}</span>
                  <span className="font-mono text-xs text-muted-foreground">{p.name}</span>
                </Command.Item>
              ))}
            </Command.Group>
          )}

          {providers.length > 0 && (
            <Command.Group heading={t("palette.providers")} className="mb-1">
              {providers.map((p) => (
                <Command.Item
                  key={p.id}
                  value={`provider ${p.id} ${p.name}`}
                  keywords={[p.id, p.name]}
                  onSelect={() => go(`/providers?sel=${encodeURIComponent(p.id)}`)}
                  className={item}
                >
                  <Layers size={14} className="flex-none text-muted-foreground" aria-hidden />
                  <span className="flex-1 truncate">{p.name}</span>
                  <span className="font-mono text-xs text-muted-foreground">{p.id}</span>
                </Command.Item>
              ))}
            </Command.Group>
          )}

          <Command.Group heading={t("palette.actions")}>
            {instances.map((inst) => {
              const id = `${inst.project}/${inst.instance}`;
              const acts = [
                { action: "run" as const, icon: Play },
                { action: "stop" as const, icon: Square },
                { action: "restart" as const, icon: RotateCcw },
                { action: "rerun" as const, icon: Zap },
              ];
              return acts.map(({ action, icon: Icon }) => (
                <Command.Item
                  key={`${action}-${id}`}
                  value={`${action} ${id}`}
                  keywords={[action, id, t(`instances.actions.${action}`)]}
                  onSelect={() => instanceAction(inst, action)}
                  className={item}
                >
                  <Icon size={14} className="flex-none text-muted-foreground" aria-hidden />
                  <span className="flex-1 truncate">
                    {t(`instances.actions.${action}`)} · {id}
                  </span>
                </Command.Item>
              ));
            })}
            {projects.map((p) => (
              <Command.Item
                key={`warmup-${p.name}`}
                value={`warmup ${p.name}`}
                keywords={["warmup", "预热", p.name]}
                onSelect={() => warmupProject(p.name)}
                className={item}
              >
                <Zap size={14} className="flex-none text-muted-foreground" aria-hidden />
                <span className="flex-1 truncate">{t("palette.warmupProject", { name: p.name })}</span>
              </Command.Item>
            ))}
            <Command.Item
              value="language"
              keywords={["language", "语言", "english", "chinese"]}
              onSelect={() => {
                setLang(lang === "zh" ? "en" : "zh");
                onClose();
              }}
              className={item}
            >
              <Globe size={14} className="flex-none text-muted-foreground" aria-hidden />
              <span className="flex-1">{t("palette.toggleLanguage")}</span>
            </Command.Item>
          </Command.Group>
        </Command.List>
      </Command>
    </div>
  );
}
