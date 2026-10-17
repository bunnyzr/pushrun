import { useEffect, useRef, useState } from "react";
import { NavLink, Outlet } from "react-router";
import { useTranslation } from "react-i18next";
import { Check, ChevronDown, Languages, Search } from "lucide-react";
import CommandPalette from "@/components/CommandPalette";
import { useLanguage } from "@/i18n";
import { fetchVersion } from "@/lib/api";
import { cn } from "@/lib/utils";

const NAV_ITEMS = [
  { to: "/projects", key: "nav.projects" },
  { to: "/instances", key: "nav.instances" },
  { to: "/providers", key: "nav.providers" },
] as const;

const LANGUAGES = [
  { id: "zh", label: "中文" },
  { id: "en", label: "English" },
] as const;

export default function AppLayout() {
  const { t } = useTranslation();
  const [lang, setLang] = useLanguage();
  const [version, setVersion] = useState<string | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [langOpen, setLangOpen] = useState(false);
  const langRef = useRef<HTMLDivElement>(null);

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

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (!langOpen) return;
    const onDown = (e: MouseEvent) => {
      if (!langRef.current?.contains(e.target as Node)) setLangOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setLangOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [langOpen]);

  return (
    <div className="flex min-h-screen flex-col bg-background">
      <header className="flex h-[52px] flex-none items-center gap-5 border-b bg-card px-5">
        <span className="text-[15px] font-bold tracking-tight">{t("app.name")}</span>
        <nav className="flex items-center gap-1">
          {NAV_ITEMS.map(({ to, key }) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                cn(
                  "rounded-md px-2.5 py-1.5 text-[13px] font-semibold transition-colors",
                  isActive
                    ? "bg-accent text-accent-foreground"
                    : "text-muted-foreground hover:bg-secondary hover:text-foreground",
                )
              }
            >
              {t(key)}
            </NavLink>
          ))}
        </nav>
        <button
          type="button"
          onClick={() => setPaletteOpen(true)}
          className="ml-2 flex items-center gap-2 rounded-md border bg-background px-2.5 py-1 text-xs text-muted-foreground hover:bg-secondary"
        >
          <Search size={12} aria-hidden="true" />
          {t("palette.hint")}
          <kbd className="rounded border bg-card px-1 font-mono text-[10px]">⌘K</kbd>
        </button>
        <div className="ml-auto flex items-center gap-3">
          {version && (
            <span className="text-xs text-muted-foreground">
              {t("common.version", { version })}
            </span>
          )}
          <div className="relative" ref={langRef}>
            <button
              type="button"
              aria-haspopup="listbox"
              aria-expanded={langOpen}
              onClick={() => setLangOpen((v) => !v)}
              className="flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium hover:bg-accent"
            >
              <Languages size={12} aria-hidden="true" />
              {LANGUAGES.find((l) => l.id === lang)?.label ?? lang}
              <ChevronDown size={12} aria-hidden="true" />
            </button>
            {langOpen && (
              <div
                role="listbox"
                aria-label={t("common.language")}
                className="absolute right-0 z-40 mt-1 w-32 overflow-hidden rounded-md border bg-popover py-1 shadow-lg"
              >
                {LANGUAGES.map((opt) => (
                  <button
                    key={opt.id}
                    type="button"
                    role="option"
                    aria-selected={lang === opt.id}
                    onClick={() => {
                      setLang(opt.id);
                      setLangOpen(false);
                    }}
                    className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-accent"
                  >
                    <Check
                      size={12}
                      aria-hidden="true"
                      className={cn(lang === opt.id ? "opacity-100" : "opacity-0")}
                    />
                    {opt.label}
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      </header>
      <main className="flex min-h-0 flex-1 flex-col">
        <Outlet />
      </main>
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
    </div>
  );
}
