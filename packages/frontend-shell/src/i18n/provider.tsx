import type { TFunction } from "i18next";
import { type ReactNode, useEffect, useLayoutEffect, useSyncExternalStore } from "react";
import { I18nextProvider, useTranslation as useReactI18nextTranslation } from "react-i18next";
import { loadCatalog } from "./catalog";
import { createLocaleFormatters, type LocaleFormatters } from "./format";
import { i18n } from "./instance";
import type { LanguagePreference, SupportedLocale } from "./locale";
import { getLanguagePreference, getLocale, setLanguagePreference, subscribeToLanguagePreference } from "./store";

export interface I18nProviderProps {
  children: ReactNode;
  /** Initial account preference. Later account changes can use setLanguagePreference. */
  preference?: LanguagePreference;
}

export const I18nProvider = ({ children, preference }: I18nProviderProps) => {
  useLayoutEffect(() => {
    setLanguagePreference(preference ?? "auto");
  }, [preference]);
  return <I18nextProvider i18n={i18n}>{children}</I18nextProvider>;
};

export const useLocale = (): SupportedLocale =>
  useSyncExternalStore(subscribeToLanguagePreference, getLocale, () => "en");

export const useI18n = (namespace = "common") => {
  const translation = useReactI18nextTranslation(namespace, { useSuspense: false });
  const locale = useLocale();
  useEffect(() => {
    void loadCatalog(namespace);
  }, [namespace]);
  return {
    ...translation,
    locale,
    preference: useSyncExternalStore(subscribeToLanguagePreference, getLanguagePreference, () => "auto"),
    setLanguagePreference,
    formatters: createLocaleFormatters(locale),
  } as {
    t: TFunction;
    i18n: typeof i18n;
    ready: boolean;
    locale: SupportedLocale;
    preference: LanguagePreference;
    setLanguagePreference: typeof setLanguagePreference;
    formatters: LocaleFormatters;
  };
};

export const useTranslation = useReactI18nextTranslation;
