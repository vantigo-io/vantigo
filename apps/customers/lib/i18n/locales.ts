export const locales = ["en", "nb"] as const;

export type Locale = (typeof locales)[number];

export const defaultLocale: Locale = "en";

export const localeNames: Record<Locale, string> = {
  en: "English",
  nb: "Norsk",
};

export const LOCALE_COOKIE = "locale";

export function isLocale(value: unknown): value is Locale {
  return typeof value === "string" && (locales as readonly string[]).includes(value);
}

/**
 * Zero-dependency Accept-Language negotiation.
 * Picks the first language (by q-value) whose primary subtag matches
 * a supported locale. Norwegian variants (no, nn) map to nb.
 */
export function negotiateLocale(acceptLanguage: string | null): Locale {
  if (!acceptLanguage) return defaultLocale;

  const ranges = acceptLanguage
    .split(",")
    .map((part) => {
      const [tag, ...params] = part.trim().split(";");
      const qParam = params.find((p) => p.trim().startsWith("q="));
      const q = qParam ? Number.parseFloat(qParam.trim().slice(2)) : 1;
      return { tag: tag.trim().toLowerCase(), q: Number.isNaN(q) ? 0 : q };
    })
    .filter((r) => r.tag && r.q > 0)
    .sort((a, b) => b.q - a.q);

  for (const { tag } of ranges) {
    const primary = tag.split("-")[0];
    if (primary === "nb" || primary === "no" || primary === "nn") return "nb";
    if (isLocale(primary)) return primary;
  }

  return defaultLocale;
}
