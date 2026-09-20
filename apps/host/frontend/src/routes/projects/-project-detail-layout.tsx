import {
  IconClock,
  IconCoin,
  IconLayoutDashboard,
  IconListCheck,
  IconReceipt,
  IconReportMoney,
  IconUsers,
} from "@tabler/icons-react";
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

type ProjectDetailView = "overview" | "tasks" | "people" | "billing" | "economy" | "time" | "expenses";

interface ProjectDetailTab extends ProjectDetailGate {
  value: ProjectDetailView;
  labelKey:
    | "project.overviewTab"
    | "project.tasksTab"
    | "project.peopleTab"
    | "project.billingTab"
    | "project.economyTab"
    | "project.timeTab"
    | "project.expensesTab";
  icon: typeof IconLayoutDashboard;
  to:
    | "/projects/$projectId"
    | "/projects/$projectId/tasks"
    | "/projects/$projectId/people"
    | "/projects/$projectId/billing"
    | "/projects/$projectId/economy"
    | "/projects/$projectId/time"
    | "/projects/$projectId/expenses";
}

/**
 * The views of the project page, each a child route, so the tab row follows
 * the URL. The first four belong to the Projects app itself, which the
 * permission guard already holds behind `projects:access`, so none of them
 * re-checks it; the module and permission fields are for the tabs other
 * modules add, the way Energy adds one to the customer page and Time adds
 * the last one here.
 */
export const projectDetailTabs: ProjectDetailTab[] = [
  {
    value: "overview",
    labelKey: "project.overviewTab",
    icon: IconLayoutDashboard,
    to: "/projects/$projectId",
  },
  {
    value: "tasks",
    labelKey: "project.tasksTab",
    icon: IconListCheck,
    to: "/projects/$projectId/tasks",
    // Tasks add no permission and no capability of their own (design §6):
    // they follow the project's roles, so whoever sees the project sees its
    // tasks, and the package's own read-only mode covers the rest.
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
  {
    // The economy view now has an hours-only half for a caller without
    // financial rights (the budget bar in hours, no amounts), so — unlike
    // delivery A — it carries no capability gate: whoever sees the project
    // sees this tab, the same rule Overview and Tasks follow. The response
    // shapes away everything the caller may not see.
    value: "economy",
    labelKey: "project.economyTab",
    icon: IconReportMoney,
    to: "/projects/$projectId/economy",
  },
  {
    // The first tab from another module: the hours logged on this project.
    // It needs the installation to have mounted time and the caller to hold
    // the app's own permission; who may see which amounts is shaped by the
    // time API per project, exactly as Billing's are by the projects API.
    value: "time",
    labelKey: "project.timeTab",
    icon: IconClock,
    to: "/projects/$projectId/time",
    module: "time",
    requiredPermissions: ["time:access"],
  },
  {
    // The second tab from another module: what has been spent on this
    // project. It carries **no** project capability — a plain member sees
    // their own expenses on it — because the expenses API decides both halves
    // on its own: the totals need financial rights on the project and answer
    // a bare 404 without them, while the list underneath keeps the visibility
    // rule every expense read has always had.
    value: "expenses",
    labelKey: "project.expensesTab",
    icon: IconReceipt,
    to: "/projects/$projectId/expenses",
    module: "expenses",
    requiredPermissions: ["expenses:access"],
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
  // Every tab but the overview is a child segment named after its value, so
  // the matched route ids say which one the URL is on; the overview is the
  // index route, and so the fallback.
  const activeTab: ProjectDetailView =
    projectDetailTabs.find(
      (tab) =>
        tab.value !== "overview" && matches.some((match) => match.routeId === `/projects/$projectId/${tab.value}`),
    )?.value ?? "overview";

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
