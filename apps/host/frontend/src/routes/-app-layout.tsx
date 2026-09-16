import { Outlet } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { appForKey, isAppEnabled } from "../apps";
import { ModuleNotEnabledPage } from "../components/errors";
import { enabledModuleKeys } from "../lib/enabled-modules";
import type { ModuleKey } from "../navigation";
import "../i18n";

/**
 * The body of every app layout route: the app's routes when its module is
 * enabled, the not-enabled page otherwise. A component check rather than a
 * beforeLoad redirect, so the URL stays put and the page can name the app.
 */
export const AppLayout = ({ app }: { app: ModuleKey }) => {
  const { t } = useI18n("host");
  const definition = appForKey(app);
  if (!isAppEnabled(definition, enabledModuleKeys())) return <ModuleNotEnabledPage appLabel={t(definition.label)} />;
  return <Outlet />;
};
