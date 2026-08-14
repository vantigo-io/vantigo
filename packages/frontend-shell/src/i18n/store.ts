import { i18n, syncDocumentLanguage } from "./instance";
import { type LanguagePreference, resolveLocale, type SupportedLocale } from "./locale";

let preference: LanguagePreference = "auto";
const listeners = new Set<() => void>();

export const getLanguagePreference = () => preference;
export const getLocale = () => resolveLocale(preference);
export const subscribeToLanguagePreference = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

export const setLanguagePreference = (next: LanguagePreference): SupportedLocale => {
  const preferenceChanged = preference !== next;
  preference = next;
  const locale = resolveLocale(next);
  const localeChanged = i18n.language !== locale;
  syncDocumentLanguage(locale);
  if (localeChanged) void i18n.changeLanguage(locale);
  if (preferenceChanged || localeChanged) {
    for (const listener of listeners) listener();
  }
  return locale;
};
