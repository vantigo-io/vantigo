import type { TFunction } from "i18next";
import { type ReactNode, useEffect, useLayoutEffect, useSyncExternalStore } from "react";
import { I18nextProvider, useTranslation as useReactI18nextTranslation } from "react-i18next";
import { loadCatalog } from "./catalog";
import { createLocaleFormatters, type LocaleFormatters } from "./format";
import { i18n } from "./instance";
import type { LanguagePreference, SupportedLocale } from "./locale";
import { getLanguagePreference, getLocale, setLanguagePreference, subscribeToLanguagePreference } from "./store";
import { getTimeZones, subscribeToTimeZones, type TimeZones } from "./time-zone";

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

/**
 * The viewer's effective time zone and the workspace's business time zone
 * (time zones design D6), re-rendering when the host sets them from the
 * session: instants are shown, and typed times read, in `timeZone`; every
 * bookkeeping day — "today", an overdue mark — is judged in `businessTimeZone`.
 */
export const useTimeZones = (): TimeZones => useSyncExternalStore(subscribeToTimeZones, getTimeZones, getTimeZones);

export const useI18n = (namespace = "common") => {
  const translation = useReactI18nextTranslation(namespace, { useSuspense: false });
  const locale = useLocale();
  const zones = useTimeZones();
  useEffect(() => {
    void loadCatalog(namespace);
  }, [namespace]);
  return {
    ...translation,
    locale,
    preference: useSyncExternalStore(subscribeToLanguagePreference, getLanguagePreference, () => "auto"),
    setLanguagePreference,
    timeZone: zones.timeZone,
    businessTimeZone: zones.businessTimeZone,
    formatters: createLocaleFormatters(locale, zones.timeZone),
  } as {
    t: TFunction;
    i18n: typeof i18n;
    ready: boolean;
    locale: SupportedLocale;
    preference: LanguagePreference;
    setLanguagePreference: typeof setLanguagePreference;
    /** The zone instants are shown in, and typed times mean: the user's own, else the workspace's. */
    timeZone: string;
    /** The workspace's business time zone, the calendar every bookkeeping day is judged in. */
    businessTimeZone: string;
    formatters: LocaleFormatters;
  };
};

export const useTranslation = useReactI18nextTranslation;
