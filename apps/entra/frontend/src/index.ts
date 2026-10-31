import "./i18n";

export { connectionQueryOptions, type EntraConnection } from "./api/connection";
export type { DirectoryPick } from "./api/directory";
export {
  DirectoryMatches,
  type DirectoryMatchesProps,
  type DirectoryMatchesUser,
} from "./components/directory-matches";
export { DirectoryPicker, type DirectoryPickerProps } from "./components/directory-picker";
export {
  OutlookConnectButton,
  type OutlookConnectButtonProps,
  OutlookConnectionCard,
  type OutlookConnectionCardProps,
  type OutlookOutcome,
} from "./components/outlook-connection";
export { entraCatalog } from "./i18n";
export { stepsDone } from "./lib/steps";
export { type EntraTab, entraTabForPath, entraTabLabelKey, entraTabPath, entraTabs } from "./lib/tabs";
export { EntraAccountsTab, type EntraAccountsTabProps } from "./pages/accounts-tab";
export { EntraConnectionTab } from "./pages/connection-tab";
/** @deprecated The four tabs are the page now; kept for one release. */
export { EntraPage, type EntraPageProps, type SignInDefaults } from "./pages/entra";
export { EntraOutlookTab } from "./pages/outlook-tab";
export { EntraStatusTab } from "./pages/status-tab";
