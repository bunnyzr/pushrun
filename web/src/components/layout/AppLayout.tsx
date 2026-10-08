import { useEffect, useState } from "react";
import { NavLink, Outlet } from "react-router";
import { useTranslation } from "react-i18next";
import { useLanguage } from "@/i18n";
import { fetchVersion } from "@/lib/api";
import { cn } from "@/lib/utils";

const NAV_ITEMS = [
  { to: "/projects", key: "nav.projects" },
  { to: "/instances", key: "nav.instances" },
  { to: "/providers", key: "nav.providers" },
] as const;

export default function AppLayout() {
  const { t } = useTranslation();
  const [lang, setLang] = useLanguage();
  const [version, setVersion] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetchVersion()
      .then((info) => {
        if (!cancelled) setVersion(info.version);
      })
      .catch(() => {
        // Version display is best-effort; the gate handles hard failures.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
          <span className="text-lg font-bold">{t("app.name")}</span>
          <nav className="flex items-center gap-1">
            {NAV_ITEMS.map(({ to, key }) => (
              <NavLink
                key={to}
                to={to}
                className={({ isActive }) =>
                  cn(
                    "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
                    isActive
                      ? "bg-accent text-accent-foreground"
                      : "text-muted-foreground hover:text-foreground",
                  )
                }
              >
                {t(key)}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3">
            {version && (
              <span className="text-xs text-muted-foreground">
                {t("common.version", { version })}
              </span>
            )}
            <button
              type="button"
              onClick={() => setLang(lang === "zh" ? "en" : "zh")}
              className="rounded-md border px-2 py-1 text-xs font-medium hover:bg-accent"
            >
              {t("common.language")}
            </button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}
