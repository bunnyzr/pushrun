import i18n from "i18next";
import { initReactI18next, useTranslation } from "react-i18next";
import { useCallback } from "react";
import en from "./en";
import zh from "./zh";

export const LANG_KEY = "pushrun.lang";

export type Language = "en" | "zh";

export function normalizeLanguage(lang: string | undefined | null): Language {
  return lang && lang.toLowerCase().startsWith("zh") ? "zh" : "en";
}

// Detection order: localStorage (pushrun.lang) → browser language.
const detector = {
  type: "languageDetector" as const,
  name: "pushrun",
  detect(): string {
    let stored: string | null;
    try {
      stored = localStorage.getItem(LANG_KEY);
    } catch {
      stored = null;
    }
    if (stored === "en" || stored === "zh") {
      return stored;
    }
    return normalizeLanguage(typeof navigator === "undefined" ? "en" : navigator.language);
  },
  cacheUserLanguage(lng: string): void {
    try {
      localStorage.setItem(LANG_KEY, normalizeLanguage(lng));
    } catch {
      // localStorage unavailable: language still changes for the session.
    }
  },
};

void i18n.use(detector).use(initReactI18next).init({
  resources: {
    en: { translation: en },
    zh: { translation: zh },
  },
  fallbackLng: "en",
  supportedLngs: ["en", "zh"],
  interpolation: { escapeValue: false },
});

export function useLanguage(): [Language, (lang: Language) => void] {
  const { i18n: instance } = useTranslation();
  const setLang = useCallback(
    (lang: Language) => {
      void instance.changeLanguage(lang);
    },
    [instance],
  );
  return [normalizeLanguage(instance.resolvedLanguage), setLang];
}

export default i18n;
