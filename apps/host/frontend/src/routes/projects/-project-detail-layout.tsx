import { IconCoin, IconLayoutDashboard, IconUsers } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Outlet, useMatches, useNavigate, useParams } from "@tanstack/react-router";
import { PageTabs, useI18n } from "@vantigo/frontend-shell";
import { type ProjectCapabilities, projectQueryOptions } from "@vantigo/projects-ui/api/projects";
import { ProjectDetailHeader } from "@vantigo/projects-ui/pages/projects.$projectId";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions, type ModuleKey } from "../../navigation";
import "../../i18n";

/** What a project-page entry needs before the caller may see it. */
interface ProjectDetailGate {
  /** The module that must be enabled for the installation; omitted = always shown. */
  module?: ModuleKey;
  /** Any one of these grants the entry; omitted = no permission needed. */
  requiredPermissions?: readonly string[];
  /** A capability the backend reports on this very project; omitted = not asked for. */
  capability?: keyof ProjectCapabilities;
}

type ProjectDetailView = "overview" | "people" | "billing";

interface ProjectDetailTab extends ProjectDetailGate {
  value: ProjectDetailView;
  labelKey: "project.overviewTab" | "project.peopleTab" | "project.billingTab";
  icon: typeof IconLayoutDashboard;
  to: "/projects/$projectId" | "/projects/$projectId/people" | "/projects/$projectId/billing";
}

/**
 * The views of the project page, each a child route, so the tab row follows
 * the URL. The three below belong to the Projects app itself, which the
 * permission guard already holds behind `projects:access`, so none of them
 * re-checks it; the module and permission fields are there for the tabs other
 * modules will add, the way Energy adds one to the customer page.
 */
export const projectDetailTabs: ProjectDetailTab[] = [
  {
    value: "overview",
    labelKey: "project.overviewTab",
    icon: IconLayoutDashboard,
    to: "/projects/$projectId",
  },
  {
    value: "people",
    labelKey: "project.peopleTab",
    icon: IconUsers,
    to: "/projects/$projectId/people",
  },
  {
    value: "billing",
    labelKey: "project.billingTab",
    icon: IconCoin,
    to: "/projects/$projectId/billing",
    // Financial fields are shaped out of the response per project, so the
    // project itself — not a permission — says whether this tab has anything
    // to show. Deep-linking it anyway renders the package's forbidden state.
    capability: "canSeeFinancials",
  },
];

const passesGate = (
  gate: ProjectDetailGate,
  enabledModules: readonly ModuleKey[] | undefined,
  permissions: string[] | undefined,
  capabilities: ProjectCapabilities | undefined,
) =>
  (gate.module === undefined || enabledModules?.includes(gate.module) === true) &&
  hasPermissions(permissions, gate.requiredPermissions) &&
  (gate.capability === undefined || capabilities?.[gate.capability] === true);

/** The tabs the caller may see: module enabled, permission granted, capability held on this project. */
export const visibleProjectDetailTabs = (
  enabledModules: readonly ModuleKey[] | undefined,
  permissions: string[] | undefined,
  capabilities: ProjectCapabilities | undefined,
): ProjectDetailTab[] => projectDetailTabs.filter((tab) => passesGate(tab, enabledModules, permissions, capabilities));

export const ProjectDetailLayout = () => {
  const { t } = useI18n("host");
  const { projectId } = useParams({ from: "/projects/$projectId" });
  const matches = useMatches();
  const navigate = useNavigate();
  // The session and authorization queries share the root layout's keys, so
  // they read its cache rather than refetching; the project is already in the
  // cache too, put there by this route's loader.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  const project = useQuery(projectQueryOptions(projectId));
  const visibleTabs = visibleProjectDetailTabs(
    enabledModuleKeys(),
    authorization.data?.permissions,
    project.data?.capabilities,
  );
  const activeTab: ProjectDetailView = matches.some((match) => match.routeId === "/projects/$projectId/billing")
    ? "billing"
    : matches.some((match) => match.routeId === "/projects/$projectId/people")
      ? "people"
      : "overview";

  return (
    <>
      <ProjectDetailHeader projectId={projectId} />
      {visibleTabs.length > 1 && (
        <PageTabs
          aria-label={t("project.views")}
          items={visibleTabs.map(({ value, labelKey, icon }) => ({ value, label: t(labelKey), icon }))}
          value={activeTab}
          onChange={(value) => {
            const tab = visibleTabs.find((item) => item.value === value);
            if (tab) void navigate({ to: tab.to, params: { projectId } });
          }}
        />
      )}
      <Outlet />
    </>
  );
};
