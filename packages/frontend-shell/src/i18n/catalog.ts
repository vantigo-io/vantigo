import { i18n } from "./instance";
import type { SupportedLocale } from "./locale";

export type TranslationCatalog = {
  readonly [key: string]: string | TranslationCatalog;
};
export type CatalogResources = {
  readonly [locale in SupportedLocale]?: TranslationCatalog;
};
export type CatalogLoader = () => Promise<CatalogResources>;

const loaders = new Map<string, CatalogLoader>();

export const registerCatalog = (namespace: string, catalogs: CatalogResources): void => {
  for (const locale of ["en", "nb"] as const) {
    const catalog = catalogs[locale];
    if (catalog) i18n.addResourceBundle(locale, namespace, catalog, true, true);
  }
};

export const registerCatalogLoader = (namespace: string, loader: CatalogLoader): void => {
  loaders.set(namespace, loader);
};

export const loadCatalog = async (namespace: string): Promise<void> => {
  const loader = loaders.get(namespace);
  if (loader) registerCatalog(namespace, await loader());
  await i18n.loadNamespaces(namespace);
};

export const loadCatalogs = async (namespaces: readonly string[]): Promise<void> => {
  await Promise.all(namespaces.map(loadCatalog));
};
