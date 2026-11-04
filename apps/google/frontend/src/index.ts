import "./i18n";

export { connectionQueryOptions, type GoogleConnection } from "./api/connection";
export {
  GoogleConnectButton,
  type GoogleConnectButtonProps,
  GoogleConnectionCard,
  type GoogleConnectionCardProps,
  type GoogleConnectionOutcome,
} from "./components/google-connection";
export { googleCatalog } from "./i18n";
export { opensOnConnection, stepsDone } from "./lib/steps";
export { type GoogleTab, googleTabForPath, googleTabLabelKey, googleTabPath, googleTabs } from "./lib/tabs";
export { GoogleAccountsTab, type GoogleAccountsTabProps, type SignInDefaults } from "./pages/accounts-tab";
export { GoogleCalendarGmailTab } from "./pages/calendar-gmail-tab";
export { GoogleConnectionTab } from "./pages/connection-tab";
export { GoogleStatusTab } from "./pages/status-tab";
