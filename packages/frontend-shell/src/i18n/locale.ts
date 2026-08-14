export const supportedLocales = ["en", "nb"] as const;
export type SupportedLocale = (typeof supportedLocales)[number];
export type LanguagePreference = "auto" | SupportedLocale;

const supportedLocale = (value: string): SupportedLocale | undefined => {
  const language = value.trim().toLowerCase().split(/[-_]/, 1)[0];
  if (language === "en") return "en";
  if (language === "nb" || language === "no" || language === "nn") return "nb";
  return undefined;
};

const browserLanguages = (): readonly string[] => {
  if (typeof navigator === "undefined") return [];
  return navigator.languages?.length ? navigator.languages : [navigator.language];
};

export const resolveLocale = (
  preference: LanguagePreference = "auto",
  languages: readonly string[] = browserLanguages(),
): SupportedLocale => {
  if (preference !== "auto") return preference;
  for (const language of languages) {
    const resolved = supportedLocale(language);
    if (resolved) return resolved;
  }
  return "en";
};

export const localeToIntlLocale = (locale: SupportedLocale): string => (locale === "nb" ? "nb-NO" : "en-US");
