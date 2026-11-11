import { useEffect, useRef, useState } from "react";
import { NavLink, Outlet, ScrollRestoration } from "react-router";
import { useTranslation } from "react-i18next";
import {
  Boxes,
  Check,
  ChevronDown,
  FolderGit2,
  Languages,
  Package,
  Search,
} from "lucide-react";
import CommandPalette from "@/components/CommandPalette";
import { useLanguage } from "@/i18n";
import { cn } from "@/lib/utils";

const NAV_ITEMS = [
  { to: "/projects", key: "nav.projects", icon: FolderGit2 },
  { to: "/instances", key: "nav.instances", icon: Boxes },
  { to: "/providers", key: "nav.providers", icon: Package },
] as const;

const LANGUAGES = [
  { id: "zh", label: "中文" },
  { id: "en", label: "English" },
] as const;

function LanguageMenu({ variant }: { variant: "rail" | "mobile" }) {
  const { t } = useTranslation();
  const [lang, setLang] = useLanguage();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const rail = variant === "rail";
  const current = LANGUAGES.find((l) => l.id === lang)?.label ?? lang;
  const visible = rail ? (lang === "zh" ? "中" : "EN") : current;
  const languageLabel = `${t("common.language")}: ${visible}`;

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        aria-label={languageLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={languageLabel}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          rail
            ? "flex h-9 w-11 items-center justify-center gap-1 rounded-md text-[11px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground"
            : "flex h-9 items-center gap-1.5 rounded-md border border-border bg-card px-2 text-xs font-medium text-muted-foreground hover:bg-secondary",
        )}
      >
        <Languages size={rail ? 15 : 13} aria-hidden="true" />
        {visible}
        {!rail && <ChevronDown size={12} aria-hidden="true" />}
      </button>
      {open && (
        <div
          role="listbox"
          aria-label={t("common.language")}
          className={cn(
            "menu-panel absolute z-50 w-32 rounded-md border bg-popover py-1 shadow-lg",
            rail ? "bottom-0 left-full ml-2" : "right-0 top-full mt-1",
          )}
        >
          {LANGUAGES.map((opt) => (
            <button
              key={opt.id}
              type="button"
              role="option"
              aria-selected={lang === opt.id}
              onClick={() => {
                setLang(opt.id);
                setOpen(false);
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
  );
}

export default function AppLayout() {
  const { t } = useTranslation();
  const [paletteOpen, setPaletteOpen] = useState(false);

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

  return (
    <div className="flex h-[100dvh] flex-col overflow-hidden bg-background md:flex-row">
      <ScrollRestoration />
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:bg-card focus:px-3 focus:py-2 focus:text-sm focus:shadow-lg"
      >
        {t("common.skipToContent")}
      </a>

      {/* Desktop rail */}
      <aside
        aria-label={t("shell.navigation")}
        className="hidden w-16 flex-none flex-col items-center border-r bg-card px-2 pt-3.5 pb-3 md:flex"
      >
        <NavLink
          to="/projects"
          aria-label={t("app.name")}
          className="mb-5 flex h-8 w-8 items-center justify-center rounded-md bg-primary text-primary-foreground"
        >
          <span className="h-0 w-0 border-y-[5px] border-l-[7px] border-y-transparent border-l-primary-foreground" />
        </NavLink>
        <nav className="flex flex-col gap-1">
          {NAV_ITEMS.map(({ to, key, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              title={t(key)}
              aria-label={t(key)}
              className={({ isActive }) =>
                cn(
                  "relative flex h-11 w-11 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                  isActive && "bg-accent text-primary",
                )
              }
            >
              {({ isActive }) => (
                <>
                  <span
                    aria-hidden="true"
                    className={cn(
                      "absolute top-2.5 bottom-2.5 left-0 w-[2px] rounded-full",
                      isActive ? "bg-primary" : "bg-transparent",
                    )}
                  />
                  <Icon size={19} strokeWidth={1.75} aria-hidden="true" />
                </>
              )}
            </NavLink>
          ))}
        </nav>
        <div className="flex-1" />
        <div className="flex flex-col items-center gap-2">
          <button
            type="button"
            aria-label={t("palette.title")}
            title={t("palette.title")}
            onClick={() => setPaletteOpen(true)}
            className="flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <Search size={16} aria-hidden="true" />
          </button>
          <LanguageMenu variant="rail" />
        </div>
      </aside>

      {/* Mobile top bar */}
      <header className="flex h-[52px] flex-none items-center gap-2 border-b bg-card px-3 md:hidden">
        <NavLink to="/projects" className="flex min-w-0 items-center gap-2">
          <span className="flex h-7 w-7 items-center justify-center rounded-md bg-primary">
            <span className="h-0 w-0 border-y-[4px] border-l-[6px] border-y-transparent border-l-primary-foreground" />
          </span>
          <span className="text-[15px] font-bold tracking-tight">{t("app.name")}</span>
        </NavLink>
        <div className="ml-auto flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => setPaletteOpen(true)}
            aria-label={t("palette.title")}
            className="flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-secondary"
          >
            <Search size={17} aria-hidden="true" />
          </button>
          <LanguageMenu variant="mobile" />
        </div>
      </header>

      <main id="main-content" className="flex min-h-0 flex-1 flex-col overflow-hidden pb-14 md:pb-0">
        <Outlet />
      </main>

      {/* Mobile bottom tabs */}
      <nav
        aria-label={t("shell.navigation")}
        className="fixed inset-x-0 bottom-0 z-40 flex h-14 items-stretch border-t bg-card pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {NAV_ITEMS.map(({ to, key, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              cn(
                "flex flex-1 flex-col items-center justify-center gap-0.5 text-[11px] font-medium text-muted-foreground",
                isActive && "text-primary",
              )
            }
          >
            {({ isActive }) => (
              <>
                <Icon size={18} strokeWidth={isActive ? 2 : 1.5} aria-hidden="true" />
                <span>{t(key)}</span>
              </>
            )}
          </NavLink>
        ))}
      </nav>

      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
    </div>
  );
}
