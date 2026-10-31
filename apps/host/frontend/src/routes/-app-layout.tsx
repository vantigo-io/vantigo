import { type NotFoundRouteProps, notFound, Outlet } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { appForKey, isAppEnabled, type ModuleWithApp } from "../apps";
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

/** The not-found a page of a module throws when the installation did not mount it; `ModuleNotFound` words it. */
export const moduleDisabledNotFound = (module: ModuleKey) =>
  notFound({ data: { moduleDisabled: module } satisfies ModuleDisabled });

/**
 * What a boundary renders for a not-found: the not-enabled page naming `labelKey`
 * (a key in the host catalog) when the not-found carries the module marker, the
 * ordinary 404 otherwise.
 */
export const ModuleNotFound = ({ labelKey, data }: { labelKey: string; data: unknown }) => {
  const { t } = useI18n("host");
  if (!isModuleDisabled(data)) return <NotFoundPage />;
  return <ModuleNotEnabledPage appLabel={t(labelKey)} />;
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
export const appLayoutOptions = (app: ModuleWithApp) => ({
  staticData: { app },
  beforeLoad: () => {
    if (!isAppEnabled(appForKey(app), enabledModuleKeys())) throw moduleDisabledNotFound(app);
  },
  notFoundComponent: ({ data }: NotFoundRouteProps) => <ModuleNotFound labelKey={appForKey(app).label} data={data} />,
  component: Outlet,
});
