import { useTranslation } from "react-i18next";
import { Dialog } from "@/components/ui/dialog";

// EnvHelpDialog documents the CI_* variables the engine injects into every
// pipeline step, so a script author never has to guess what $CI_PORT is.
export default function EnvHelpDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const vars: { name: string; key: string }[] = [
    { name: "CI_WORKSPACE", key: "workspace" },
    { name: "CI_PROJECT_DIR", key: "projectDir" },
    { name: "CI_PROJECT_NAME", key: "projectName" },
    { name: "CI_TRIGGER_PROJECT_DIR", key: "triggerDir" },
    { name: "CI_TRIGGER_PROJECT_NAME", key: "triggerName" },
    { name: "CI_PORT", key: "port" },
    { name: "CI_PORT_<NAME>", key: "namedPort" },
    { name: "CI_COMMIT", key: "commit" },
    { name: "CI_BRANCH", key: "branch" },
    { name: "CI_USER", key: "user" },
    { name: "CI_RUN_ID", key: "runId" },
    { name: "CI_PROJECT", key: "project" },
    { name: "CI_INSTANCE", key: "instance" },
  ];
  return (
    <Dialog
      open={open}
      title={t("projects.envHelp.title")}
      footer={
        <button
          type="button"
          onClick={onClose}
          className="rounded-md border px-3 py-1.5 text-sm hover:bg-accent"
        >
          {t("common.dismiss")}
        </button>
      }
    >
      <p className="mb-3 text-sm text-muted-foreground">{t("projects.envHelp.intro")}</p>
      <dl className="max-h-[50vh] space-y-2 overflow-y-auto">
        {vars.map(({ name, key }) => (
          <div key={name}>
            <dt className="font-mono text-xs font-semibold">${name}</dt>
            <dd className="text-xs text-muted-foreground">{t(`projects.envHelp.vars.${key}`)}</dd>
          </div>
        ))}
      </dl>
    </Dialog>
  );
}
