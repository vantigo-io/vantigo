import i18next from "i18next";
import { initReactI18next } from "react-i18next";
import { commonCatalog } from "./catalogs/common";
import { settingsCatalog } from "./catalogs/settings";
import { diagnosticsEnabled, reportI18nProblem } from "./diagnostics";
import { resolveLocale, type SupportedLocale } from "./locale";

const initialLocale = resolveLocale();

const documentLocale = (locale: string): SupportedLocale => {
  const language = locale.trim().toLowerCase().split(/[-_]/, 1)[0];
  return language === "nb" || language === "no" || language === "nn" ? "nb" : "en";
};

export const syncDocumentLanguage = (locale: string): void => {
  if (typeof document === "undefined") return;
  const language = documentLocale(locale);
  if (document.documentElement.lang !== language) document.documentElement.lang = language;
};

export const i18n = i18next.createInstance();
i18n.on("languageChanged", syncDocumentLanguage);
syncDocumentLanguage(initialLocale);

void i18n.use(initReactI18next).init({
  lng: initialLocale,
  fallbackLng: "en",
  supportedLngs: ["en", "nb"],
  defaultNS: "common",
  ns: ["common", "settings"],
  resources: {
    en: { common: commonCatalog.en, settings: settingsCatalog.en },
    nb: { common: commonCatalog.nb, settings: settingsCatalog.nb },
  },
  interpolation: { escapeValue: false },
  saveMissing: diagnosticsEnabled(),
  missingKeyHandler: (_lngs, namespace, key) => reportI18nProblem(`Missing translation: ${namespace}:${key}`),
  missingInterpolationHandler: (text, value) =>
    reportI18nProblem(`Missing interpolation value "${value}" in translation "${text}"`),
});
