import { type NotFoundRouteProps, notFound, Outlet } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { appForKey, isAppEnabled } from "../apps";
import { ModuleNotEnabledPage, NotFoundPage } from "../components/errors";
import { enabledModuleKeys } from "../lib/enabled-modules";
import type { ModuleKey } from "../navigation";
import "../i18n";

/** The not-found payload the gate throws, so the boundary can tell "module off" from a genuine 404 below it. */
interface ModuleDisabled {
  moduleDisabled: ModuleKey;
}

const isModuleDisabled = (data: unknown): data is ModuleDisabled =>
  typeof data === "object" && data !== null && "moduleDisabled" in data;

const AppNotFound = ({ app, data }: { app: ModuleKey; data: unknown }) => {
  const { t } = useI18n("host");
  if (!isModuleDisabled(data)) return <NotFoundPage />;
  return <ModuleNotEnabledPage appLabel={t(appForKey(app).label)} />;
};

/**
 * Route options shared by every app layout route. The enablement gate lives
 * in `beforeLoad`: when the module is turned off it throws a not-found that
 * carries a marker, and the router resolves it against this route's own
 * boundary. That keeps the URL in place, renders the not-enabled page naming
 * the app, and — because loading stops at the boundary — never runs a child
 * loader, so no request reaches a module the installation did not mount. A
 * genuine 404 thrown by a child loader reaches the same boundary without the
 * marker and renders the ordinary not-found page.
 */
export const appLayoutOptions = (app: ModuleKey) => ({
  staticData: { app },
  beforeLoad: () => {
    if (!isAppEnabled(appForKey(app), enabledModuleKeys())) {
      throw notFound({ data: { moduleDisabled: app } satisfies ModuleDisabled });
    }
  },
  notFoundComponent: ({ data }: NotFoundRouteProps) => <AppNotFound app={app} data={data} />,
  component: Outlet,
});
