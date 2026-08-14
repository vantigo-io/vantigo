export {
  type AppConfig,
  type AppSupport,
  appConfig,
  appUrl,
  hasSupportContact,
  initAppConfig,
  runtimeBase,
} from "./app-config";
export { AppShellLayout, type AppShellLayoutProps, type ShellUser, SupportContactLine } from "./app-shell-layout";
export {
  type CatalogLoader,
  type CatalogResources,
  createLocaleFormatters,
  formatCurrency,
  formatDate,
  formatNumber,
  getLanguagePreference,
  getLocale,
  I18nProvider,
  type I18nProviderProps,
  i18n,
  type LanguagePreference,
  type LocaleFormatters,
  loadCatalog,
  loadCatalogs,
  registerCatalog,
  registerCatalogLoader,
  resolveLocale,
  type SupportedLocale,
  setLanguagePreference,
  useI18n,
  useLocale,
  useTranslation,
} from "./i18n";
export { vantigoLogo } from "./logo";
export { PageHeader, type PageHeaderProps } from "./page-header";
export { SpotlightSearchBox } from "./spotlight-search-box";
export { vantigoTheme } from "./theme";
