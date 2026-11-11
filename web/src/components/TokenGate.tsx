import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ApiError, apiFetch, fetchVersion, setUnauthorizedHandler } from "@/lib/api";
import { tokenStore } from "@/lib/auth";
import type { Project } from "@/lib/types";

type GateState = "loading" | "open" | "gate" | "unreachable";

export default function TokenGate({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const [state, setState] = useState<GateState>("loading");
  const [token, setToken] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      tokenStore.clear();
      setState("gate");
      setToken("");
    });
    return () => setUnauthorizedHandler(null);
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      let version;
      try {
        version = await fetchVersion();
      } catch {
        if (!cancelled) setState("unreachable");
        return;
      }
      if (!version.auth.enabled) {
        if (!cancelled) setState("open");
        return;
      }
      if (!tokenStore.get()) {
        if (!cancelled) setState("gate");
        return;
      }
      try {
        await apiFetch<Project[]>("/projects");
        if (!cancelled) setState("open");
      } catch (err) {
        // A 401 already moved us back to the gate via the unauthorized handler.
        if (!cancelled && !(err instanceof ApiError && err.status === 401)) {
          setState("unreachable");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const onSubmit = useCallback(
    async (e: FormEvent) => {
      e.preventDefault();
      const value = token.trim();
      if (!value || submitting) return;
      setSubmitting(true);
      setError(null);
      tokenStore.set(value);
      try {
        await apiFetch<Project[]>("/projects");
        setState("open");
      } catch (err) {
        tokenStore.clear();
        setState("gate");
        setError(err instanceof ApiError ? t("gate.invalidToken") : t("gate.unreachable"));
      } finally {
        setSubmitting(false);
      }
    },
    [token, submitting, t],
  );

  if (state === "open") {
    return <>{children}</>;
  }

  return (
    <div className="console-content flex min-h-[100dvh] items-center justify-center bg-background px-4">
      <div className="w-full max-w-[380px]">
        <div className="mb-5 flex items-center gap-2.5">
          <span className="flex h-8 w-8 items-center justify-center rounded-md bg-primary">
            <span className="h-0 w-0 border-y-[5px] border-l-[7px] border-y-transparent border-l-primary-foreground" />
          </span>
          <span className="text-[15px] font-semibold tracking-[-0.02em]">{t("app.name")}</span>
        </div>
        <div className="rounded-md border bg-card p-6">
          {state === "loading" && (
            <p className="text-sm text-muted-foreground">{t("gate.loading")}</p>
          )}
          {state === "unreachable" && (
            <p className="text-sm text-destructive">{t("gate.unreachable")}</p>
          )}
          {state === "gate" && (
            <form onSubmit={onSubmit} className="space-y-5">
              <div className="space-y-1.5">
                <div className="eyebrow">{t("gate.eyebrow")}</div>
                <h1 className="text-[19px] font-semibold tracking-[-0.02em]">{t("gate.title")}</h1>
                <p className="text-[12.5px] leading-5 text-muted-foreground">
                  {t("gate.description")}
                </p>
              </div>
              <div className="space-y-2">
                <label htmlFor="pushrun-token" className="text-sm font-medium">
                  {t("gate.tokenLabel")}
                </label>
                <input
                  id="pushrun-token"
                  type="password"
                  autoComplete="off"
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  placeholder={t("gate.tokenPlaceholder")}
                  className="h-10 w-full rounded-md border border-input bg-background px-3 font-mono text-sm outline-none focus:ring-2 focus:ring-ring"
                />
              </div>
              <p className="rounded-md border bg-secondary/60 px-3 py-2 text-[11px] leading-5 text-muted-foreground">
                {t("gate.hint")}
              </p>
              {error && <p className="text-sm text-destructive">{error}</p>}
              <button
                type="submit"
                disabled={submitting || token.trim() === ""}
                className="h-10 w-full rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary-hover disabled:opacity-50"
              >
                {submitting ? t("gate.checking") : t("gate.submit")}
              </button>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}
