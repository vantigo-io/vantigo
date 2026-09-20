import { AreaChart, BarChart, DonutChart } from "@mantine/charts";
import {
  Alert,
  Anchor,
  Card,
  Grid,
  Group,
  RingProgress,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Text,
  ThemeIcon,
} from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import {
  IconAlertCircle,
  IconBolt,
  IconBriefcase,
  IconChecklist,
  IconCircleCheck,
  IconClock,
  IconInbox,
  IconMessage,
  IconPackage,
  IconReceipt2,
  IconRefreshAlert,
  IconUsers,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { KpiCard, WidgetCard } from "@vantigo/frontend-shell/ui";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { request } from "../api/request";
import { enabledModuleKeys } from "../lib/enabled-modules";
import { hasPermissions, type ModuleKey } from "../navigation";
import "../i18n";

type DashboardPreset = "7d" | "30d" | "90d" | "12m" | "custom";
type DateRange = { from: Date; to: Date };

/** The preset validateSearch below falls back to when no search params are given. */
export const DEFAULT_DASHBOARD_PRESET: DashboardPreset = "30d";

interface DailyPoint {
  date: string;
  value: number;
}

interface CustomerSummary {
  from: string;
  to: string;
  totalActiveCustomers: number;
  totalActiveCustomersDelta: number;
  newCustomers: number;
  newCustomersDelta: number;
  newContacts: number;
  newContactsDelta: number;
}

interface CommunicationsSummary {
  from: string;
  to: string;
  openConversations: number;
  openConversationsDelta: number;
  newConversations: number;
  newConversationsDelta: number;
  messages: number;
  messagesDelta: number;
  closedConversations: number;
  closedConversationsDelta: number;
}

interface ProductsSummary {
  from: string;
  to: string;
  totalActiveProducts: number;
  totalActiveProductsDelta: number;
  newProducts: number;
  newProductsDelta: number;
  statusCounts: Record<string, number>;
  statusCountDeltas: Record<string, number>;
}

interface EnergySummary {
  from: string;
  to: string;
  meteringPointCount: number;
  meteringPointCountDelta: number;
  activeSupplyPeriods: number;
  activeSupplyPeriodsDelta: number;
  consumptionKwh: number;
  consumptionKwhDelta: number;
  previousConsumptionKwh: number;
}

interface ProjectsSummary {
  from: string;
  to: string;
  activeProjects: number;
  activeProjectsDelta: number;
  newProjects: number;
  newProjectsDelta: number;
  /** Ready milestones the caller has financial rights on — a state, not a delta. */
  readyMilestones: number;
}

interface TimeSummary {
  from: string;
  to: string;
  hoursThisWeek: number;
  hoursThisWeekDelta: number;
  awaitingMyApproval: number;
  awaitingMyApprovalDelta: number;
}

interface ExpensesSummary {
  from: string;
  to: string;
  awaitingMyApproval: number;
  awaitingMyApprovalDelta: number;
  myDrafts: number;
  /** Never summed across currencies (design §4): one line per currency. */
  myUnreimbursed: { currency: string; amount: number }[];
}

interface AttentionItem {
  id: string;
  type: string;
  title: string;
  occurredAt: string;
  entityId: string;
  /**
   * Optional, added by the Expenses module: a number a server-built title
   * cannot localise, so the host renders its own sentence from `type` and
   * this count instead of the server's English fallback.
   */
  count?: number;
}

const moduleCards = [
  {
    title: "dashboard.customers",
    description: "dashboard.manageCustomers",
    path: "/customers",
    icon: IconUsers,
    module: "customers" as ModuleKey,
    requiredPermissions: ["customers:view"],
  },
  {
    title: "dashboard.communications",
    description: "dashboard.reviewMessages",
    path: "/communications/inbox",
    icon: IconMessage,
    module: "communications" as ModuleKey,
    requiredPermissions: ["communications:conversations-view"],
  },
  {
    title: "dashboard.products",
    description: "dashboard.manageProducts",
    path: "/products",
    icon: IconPackage,
    module: "products" as ModuleKey,
    requiredPermissions: [
      "products:products-view",
      "products:variants-view",
      "products:pricing-view",
      "products:categories-view",
      "products:tax-categories-view",
    ],
  },
  {
    title: "dashboard.energy",
    description: "dashboard.manageEnergy",
    path: "/energy/metering-points",
    icon: IconBolt,
    module: "energy" as ModuleKey,
    requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
  },
  {
    title: "dashboard.projects",
    description: "dashboard.manageProjects",
    path: "/projects",
    icon: IconBriefcase,
    module: "projects" as ModuleKey,
    requiredPermissions: ["projects:access"],
  },
  {
    title: "dashboard.time",
    description: "dashboard.manageTime",
    path: "/time",
    icon: IconClock,
    module: "time" as ModuleKey,
    requiredPermissions: ["time:access"],
  },
  {
    title: "dashboard.expenses",
    description: "dashboard.manageExpenses",
    path: "/expenses",
    icon: IconReceipt2,
    module: "expenses" as ModuleKey,
    requiredPermissions: ["expenses:access"],
  },
] as const;

const presetDays: Record<Exclude<DashboardPreset, "custom">, number> = {
  "7d": 7,
  "30d": 30,
  "90d": 90,
  "12m": 365,
};

const metrics = [
  { module: "customers" as ModuleKey, metric: "newCustomers", color: "blue.6", label: "dashboard.newCustomers" },
  {
    module: "communications" as ModuleKey,
    metric: "newConversations",
    color: "violet.6",
    label: "dashboard.newConversations",
  },
  { module: "products" as ModuleKey, metric: "newProducts", color: "orange.6", label: "dashboard.newProducts" },
  { module: "energy" as ModuleKey, metric: "consumptionKwh", color: "teal.6", label: "dashboard.consumption" },
  { module: "projects" as ModuleKey, metric: "newProjects", color: "grape.6", label: "dashboard.newProjects" },
  { module: "time" as ModuleKey, metric: "hours", color: "cyan.6", label: "dashboard.hoursLogged" },
  { module: "expenses" as ModuleKey, metric: "netAmount", color: "pink.6", label: "dashboard.netExpenses" },
] as const;

const dateOnly = (value: Date) => value.toISOString().slice(0, 10);

const startOfDay = (value: Date) => {
  const result = new Date(value);
  result.setHours(0, 0, 0, 0);
  return result;
};

const endOfDay = (value: Date) => {
  const result = new Date(value);
  result.setHours(23, 59, 59, 999);
  return result;
};

const presetRange = (preset: Exclude<DashboardPreset, "custom">): DateRange => {
  const to = endOfDay(new Date());
  const from = startOfDay(new Date(to));
  from.setDate(from.getDate() - presetDays[preset]);
  return { from, to };
};

const buildStatsUrl = (module: ModuleKey, endpoint: "summary" | "timeseries", range: DateRange, metric?: string) => {
  const params = new URLSearchParams({ from: range.from.toISOString(), to: range.to.toISOString() });
  if (metric) params.set("metric", metric);
  return `/api/v1/${module}/stats/${endpoint}?${params}`;
};

const fetchSummary = <T,>(module: ModuleKey, range: DateRange, signal: AbortSignal) =>
  request<T>(buildStatsUrl(module, "summary", range), { signal });

const fetchTimeseries = (module: ModuleKey, metric: string, range: DateRange, signal: AbortSignal) =>
  request<DailyPoint[]>(buildStatsUrl(module, "timeseries", range, metric), { signal });

const fetchAttention = async (module: ModuleKey, signal: AbortSignal) => {
  try {
    return await request<AttentionItem[]>(`/api/v1/${module}/stats/attention`, { signal });
  } catch {
    // Attention is deliberately best-effort: a module can be enabled while a
    // user lacks the more specific permission required by this endpoint.
    return [];
  }
};

/** The four project-economy attention types, which all link to the Economy tab. */
const projectEconomyAttentionTypes = ["budgetWarning", "budgetExceeded", "milestoneReady", "milestoneOverdue"];

/**
 * Where an attention item leads. Every module agrees on `type` and
 * `entityId`, so this is the one place that turns the pair into a URL.
 * Time's two ids were decided with its stats contract: an unsubmitted week
 * carries its Monday, and a waiting approval carries the queue's group key
 * `<userId>/<Monday>`, which is not addressable — the queue is.
 */
export const attentionHref = (item: { module: ModuleKey; type: string; entityId: string }) => {
  if (item.module === "communications" && item.type === "conversationNoReply") {
    return `/communications/inbox?conversationId=${encodeURIComponent(item.entityId)}`;
  }
  if (item.module === "customers") return `/customers/${encodeURIComponent(item.entityId)}`;
  if (item.module === "products") return `/products/${encodeURIComponent(item.entityId)}`;
  if (item.module === "energy") return `/energy/metering-points/${encodeURIComponent(item.entityId)}`;
  if (item.module === "projects") {
    // The two budget types carry a bare project id, same as projectOverdue;
    // the two milestone types carry `<projectId>/<milestoneId>`, which is the
    // milestone's own id, not a URL — encoding the pair whole (as the bare
    // project case below does) would turn the slash into %2F and break the
    // link, so the project id is taken as the part before it. A malformed id
    // (empty, or with no project part) must not reach the router as
    // `/projects//economy`; the app's own list is the safest fallback for
    // anything this build cannot address precisely.
    if (projectEconomyAttentionTypes.includes(item.type)) {
      const projectId = item.entityId.split("/")[0];
      return projectId ? `/projects/${encodeURIComponent(projectId)}/economy` : "/projects";
    }
    return `/projects/${encodeURIComponent(item.entityId)}`;
  }
  if (item.module === "time") {
    if (item.type === "weekUnsubmitted") return `/time?week=${encodeURIComponent(item.entityId)}`;
    if (item.type === "approvalWaiting") return "/time/approvals";
    // A type this build does not know: the app's home is the one page that is
    // right for any of them, and is certainly not the approval queue.
    return "/time";
  }
  if (item.module === "expenses") {
    if (item.type === "approvalWaiting") return "/expenses/approvals";
    if (item.type === "expenseRejected") {
      // A rejected unit is either a standalone expense, whose entity id is a
      // bare number, or a whole travel claim, whose id is `claim/<id>` —
      // the two units number independently, so a bare id would collide. The
      // link keys on the prefix rather than on parsing the number, and a
      // prefixed id with nothing after it falls back to the list.
      const claimId = item.entityId.startsWith("claim/") ? item.entityId.slice("claim/".length) : undefined;
      if (claimId !== undefined) {
        return claimId ? `/expenses/claims/${encodeURIComponent(claimId)}` : "/expenses?status=rejected";
      }
      return "/expenses?status=rejected";
    }
    if (item.type === "reimbursementWaiting") return "/expenses/reimbursements";
    // A type this build does not know: My expenses is the one page that is
    // right for any of them.
    return "/expenses";
  }
  return "/communications/inbox";
};

const projectAttentionTitleKeys: Record<string, string> = {
  budgetWarning: "dashboard.projectBudgetWarning",
  budgetExceeded: "dashboard.projectBudgetExceeded",
  milestoneReady: "dashboard.projectMilestoneReady",
  milestoneOverdue: "dashboard.projectMilestoneOverdue",
};

/**
 * The catalog key that names an item, or undefined for an item whose own
 * `title` the server already wrote. Time's titles are built from data rather
 * than from a catalog, so they arrive in English; naming them again here is
 * what puts them in the reader's language. The four project-economy types are
 * server-built too (a project or a milestone name), so they take the same
 * treatment, this time with the name filled into the sentence rather than a
 * date.
 */
export const attentionTitleKey = (item: { module: ModuleKey; type: string; count?: number; entityId?: string }) => {
  if (item.module === "time") {
    if (item.type === "weekUnsubmitted") return "dashboard.timeWeekUnsubmitted";
    if (item.type === "approvalWaiting") return "dashboard.timeApprovalWaiting";
    return undefined;
  }
  if (item.module === "projects") return projectAttentionTitleKeys[item.type];
  if (item.module === "expenses") {
    // A rejected unit is an expense or a whole trip, and the two are called
    // different things. The link already keys on the `claim/` prefix; so does
    // the sentence, rather than calling a trip "your expense".
    if (item.type === "expenseRejected") {
      return item.entityId?.startsWith("claim/") ? "dashboard.claimRejected" : "dashboard.expenseRejected";
    }
    // The count is the server's, sent because a title it builds cannot be
    // localised with a number baked in; when it is present the sentence
    // names it, and when it is not (a caller with exactly one waiting) the
    // shorter form still reads naturally.
    if (item.type === "approvalWaiting") {
      return item.count ? "dashboard.expenseApprovalWaitingCount" : "dashboard.expenseApprovalWaiting";
    }
    if (item.type === "reimbursementWaiting") return "dashboard.expenseReimbursementWaiting";
    return undefined;
  }
  return undefined;
};

/** The Monday a time item is about: alone in the id, or after the user id. */
export const attentionWeek = (entityId: string) => entityId.slice(entityId.indexOf("/") + 1);

/**
 * What an attention item is called: the module's own title, or — for time,
 * whose titles the server builds from data and therefore in English — the
 * catalog's, naming the week the item is about.
 *
 * The week is a plain calendar date, so it is formatted **in UTC**: west of
 * Greenwich a local rendering of `2026-08-31T00:00Z` is the 30th, and the
 * title would then name a different week from the one the link opens.
 */
export const attentionTitle = (
  item: { module: ModuleKey; type: string; entityId: string; title: string; count?: number },
  t: (key: string, values?: Record<string, unknown>) => string,
  formatDate: (value: string, options: Intl.DateTimeFormatOptions) => string,
) => {
  const key = attentionTitleKey(item);
  if (!key) return item.title;
  // Projects' and expenses' titles are both server-built from data (a
  // project's, a milestone's or an expense's own name, or nothing at all for
  // the payroll item), not from a catalog, so both name the item and — for
  // expenses — the count the server sent, if any. Time's is the odd one out:
  // its entityId carries the week the title is about, not a name.
  if (item.module === "projects" || item.module === "expenses") return t(key, { name: item.title, count: item.count });
  return t(key, { date: formatDate(attentionWeek(item.entityId), { dateStyle: "medium", timeZone: "UTC" }) });
};

/**
 * The Projects card's "N milestones ready to invoice" hint, or nothing when
 * there is nothing ready. It is a state, not a delta (milestones may be in
 * several currencies, so there is no one comparable total), and zero is the
 * ordinary case for most projects, so a standing zero would be a permanent
 * fixture rather than something worth reading — the same reasoning as Time's
 * approval hint.
 */
export const readyMilestonesHint = (
  count: number | undefined,
  t: (key: string, values?: Record<string, unknown>) => string,
): string | undefined => (count ? t("dashboard.readyMilestonesHint", { count }) : undefined);

/**
 * The Projects card's hint, in full: the ready-milestones hint when there is
 * something ready — the more actionable of the two figures — and otherwise
 * exactly the "N new projects" hint the card showed before this feature, so
 * the card never goes from always saying something to saying nothing.
 */
export const projectsCardHint = (
  readyMilestones: number | undefined,
  newProjects: number | undefined,
  t: (key: string, values?: Record<string, unknown>) => string,
): string => readyMilestonesHint(readyMilestones, t) ?? t("dashboard.newProjectsHint", { count: newProjects ?? 0 });

/**
 * The Time card's "N waiting for your approval" hint, or nothing when there
 * is nothing to approve. Zero is the ordinary case for most `time:access`
 * holders — they approve nobody's hours — so a standing zero would be a
 * permanent fixture rather than something worth reading.
 */
export const awaitingApprovalHint = (
  count: number | undefined,
  t: (key: string, values?: Record<string, unknown>) => string,
): string | undefined => (count ? t("dashboard.awaitingApprovalHint", { count }) : undefined);

/**
 * The Expenses card's primary figure: the first currency the caller is owed
 * in, with how many more there are when they are owed in several — nothing is
 * ever summed across currencies (design §4) — and a plain "nothing owed" for
 * an empty list, or for no list at all. The caller shows "—" while the summary
 * is still loading, the way every other card does, and only asks this once
 * there is an answer.
 */
export const expensesUnreimbursedValue = (
  totals: { currency: string; amount: number }[] | undefined,
  formatCurrency: (value: number, currency: string) => string,
  t: (key: string, values?: Record<string, unknown>) => string,
): string => {
  if (!totals || totals.length === 0) return t("dashboard.nothingOwed");
  const [first, ...rest] = totals;
  const base = formatCurrency(first.amount, first.currency);
  return rest.length ? `${base} ${t("dashboard.moreCurrencies", { count: rest.length })}` : base;
};

const deltaPercent = (current: number, absoluteDelta: number) => {
  const previous = current - absoluteDelta;
  if (previous === 0) return absoluteDelta === 0 ? 0 : absoluteDelta > 0 ? 100 : -100;
  return Math.round((absoluteDelta / Math.abs(previous)) * 100);
};

const sparkline = (points: DailyPoint[] | undefined) => points?.map((point) => point.value);

const relativeTime = (value: string, locale: string) => {
  const elapsedSeconds = (Date.now() - new Date(value).getTime()) / 1000;
  const absoluteSeconds = Math.abs(elapsedSeconds);
  const [amount, unit] =
    absoluteSeconds < 60
      ? [Math.round(elapsedSeconds), "second"]
      : absoluteSeconds < 3600
        ? [Math.round(elapsedSeconds / 60), "minute"]
        : absoluteSeconds < 86400
          ? [Math.round(elapsedSeconds / 3600), "hour"]
          : [Math.round(elapsedSeconds / 86400), "day"];
  return new Intl.RelativeTimeFormat(locale === "nb" ? "nb-NO" : "en-US", { numeric: "auto" }).format(
    amount,
    unit as Intl.RelativeTimeFormatUnit,
  );
};

const DashboardPage = () => {
  const { t, formatters, locale } = useI18n("host");
  const search = Route.useSearch();
  const navigate = useNavigate({ from: "/dashboard" });
  const { data: session } = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session,
    retry: false,
    staleTime: 300_000,
  });

  const enabledModules = enabledModuleKeys();
  const permissions = authorization.data?.permissions;
  const modules = moduleCards.filter(
    (card) => enabledModules.includes(card.module) && hasPermissions(permissions, card.requiredPermissions),
  );
  const allowed = (module: ModuleKey) => modules.some((item) => item.module === module);
  const range =
    search.preset === "custom" && search.from && search.to
      ? { from: startOfDay(new Date(`${search.from}T00:00:00`)), to: endOfDay(new Date(`${search.to}T00:00:00`)) }
      : presetRange((search.preset === "custom" ? "30d" : search.preset) as Exclude<DashboardPreset, "custom">);

  const customersSummary = useQuery({
    queryKey: ["dashboard", "customers", "summary", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchSummary<CustomerSummary>("customers", range, signal),
    enabled: allowed("customers"),
    retry: false,
  });
  const communicationsSummary = useQuery({
    queryKey: ["dashboard", "communications", "summary", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchSummary<CommunicationsSummary>("communications", range, signal),
    enabled: allowed("communications"),
    retry: false,
  });
  const productsSummary = useQuery({
    queryKey: ["dashboard", "products", "summary", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchSummary<ProductsSummary>("products", range, signal),
    enabled: allowed("products"),
    retry: false,
  });
  const energySummary = useQuery({
    queryKey: ["dashboard", "energy", "summary", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchSummary<EnergySummary>("energy", range, signal),
    enabled: allowed("energy"),
    retry: false,
  });

  const projectsSummary = useQuery({
    queryKey: ["dashboard", "projects", "summary", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchSummary<ProjectsSummary>("projects", range, signal),
    enabled: allowed("projects"),
    retry: false,
  });

  const timeSummary = useQuery({
    queryKey: ["dashboard", "time", "summary", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchSummary<TimeSummary>("time", range, signal),
    enabled: allowed("time"),
    retry: false,
  });

  const expensesSummary = useQuery({
    queryKey: ["dashboard", "expenses", "summary", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchSummary<ExpensesSummary>("expenses", range, signal),
    enabled: allowed("expenses"),
    retry: false,
  });

  const customersTimeseries = useQuery({
    queryKey: [
      "dashboard",
      "customers",
      "timeseries",
      "newCustomers",
      range.from.toISOString(),
      range.to.toISOString(),
    ],
    queryFn: ({ signal }) => fetchTimeseries("customers", "newCustomers", range, signal),
    enabled: allowed("customers"),
    retry: false,
  });
  const communicationsTimeseries = useQuery({
    queryKey: [
      "dashboard",
      "communications",
      "timeseries",
      "newConversations",
      range.from.toISOString(),
      range.to.toISOString(),
    ],
    queryFn: ({ signal }) => fetchTimeseries("communications", "newConversations", range, signal),
    enabled: allowed("communications"),
    retry: false,
  });
  const productsTimeseries = useQuery({
    queryKey: ["dashboard", "products", "timeseries", "newProducts", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchTimeseries("products", "newProducts", range, signal),
    enabled: allowed("products"),
    retry: false,
  });
  const energyTimeseries = useQuery({
    queryKey: ["dashboard", "energy", "timeseries", "consumptionKwh", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchTimeseries("energy", "consumptionKwh", range, signal),
    enabled: allowed("energy"),
    retry: false,
  });

  const projectsTimeseries = useQuery({
    queryKey: ["dashboard", "projects", "timeseries", "newProjects", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchTimeseries("projects", "newProjects", range, signal),
    enabled: allowed("projects"),
    retry: false,
  });

  const timeTimeseries = useQuery({
    queryKey: ["dashboard", "time", "timeseries", "hours", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchTimeseries("time", "hours", range, signal),
    enabled: allowed("time"),
    retry: false,
  });

  const expensesTimeseries = useQuery({
    queryKey: ["dashboard", "expenses", "timeseries", "netAmount", range.from.toISOString(), range.to.toISOString()],
    queryFn: ({ signal }) => fetchTimeseries("expenses", "netAmount", range, signal),
    enabled: allowed("expenses"),
    retry: false,
  });

  const customersAttention = useQuery({
    queryKey: ["dashboard", "customers", "attention"],
    queryFn: ({ signal }) => fetchAttention("customers", signal),
    enabled: allowed("customers"),
    retry: false,
  });
  const communicationsAttention = useQuery({
    queryKey: ["dashboard", "communications", "attention"],
    queryFn: ({ signal }) => fetchAttention("communications", signal),
    enabled: allowed("communications"),
    retry: false,
  });
  const productsAttention = useQuery({
    queryKey: ["dashboard", "products", "attention"],
    queryFn: ({ signal }) => fetchAttention("products", signal),
    enabled: allowed("products"),
    retry: false,
  });
  const energyAttention = useQuery({
    queryKey: ["dashboard", "energy", "attention"],
    queryFn: ({ signal }) => fetchAttention("energy", signal),
    enabled: allowed("energy"),
    retry: false,
  });

  const projectsAttention = useQuery({
    queryKey: ["dashboard", "projects", "attention"],
    queryFn: ({ signal }) => fetchAttention("projects", signal),
    enabled: allowed("projects"),
    retry: false,
  });

  const timeAttention = useQuery({
    queryKey: ["dashboard", "time", "attention"],
    queryFn: ({ signal }) => fetchAttention("time", signal),
    enabled: allowed("time"),
    retry: false,
  });

  const expensesAttention = useQuery({
    queryKey: ["dashboard", "expenses", "attention"],
    queryFn: ({ signal }) => fetchAttention("expenses", signal),
    enabled: allowed("expenses"),
    retry: false,
  });

  const timeseriesByMetric: Record<string, DailyPoint[] | undefined> = {
    "customers:newCustomers": customersTimeseries.data,
    "communications:newConversations": communicationsTimeseries.data,
    "products:newProducts": productsTimeseries.data,
    "energy:consumptionKwh": energyTimeseries.data,
    "projects:newProjects": projectsTimeseries.data,
    "time:hours": timeTimeseries.data,
    "expenses:netAmount": expensesTimeseries.data,
  };
  const timeseriesQueries: Record<string, { isPending: boolean; isError: boolean }> = {
    "customers:newCustomers": customersTimeseries,
    "communications:newConversations": communicationsTimeseries,
    "products:newProducts": productsTimeseries,
    "energy:consumptionKwh": energyTimeseries,
    "projects:newProjects": projectsTimeseries,
    "time:hours": timeTimeseries,
    "expenses:netAmount": expensesTimeseries,
  };
  const availableMetrics = metrics.filter((metric) => allowed(metric.module));
  const defaultMetric = availableMetrics[0];
  const requestedMetric = search.metric ?? "";
  const selectedMetricValue =
    defaultMetric &&
    metrics.some((metric) => `${metric.module}:${metric.metric}` === requestedMetric && allowed(metric.module))
      ? requestedMetric
      : defaultMetric
        ? `${defaultMetric.module}:${defaultMetric.metric}`
        : "";
  const selectedMetric = metrics.find((metric) => `${metric.module}:${metric.metric}` === selectedMetricValue);
  const selectedTimeseries = selectedMetric ? timeseriesByMetric[selectedMetricValue] : undefined;
  const selectedTimeseriesQuery = selectedMetric ? timeseriesQueries[selectedMetricValue] : undefined;
  const attentionLoading =
    (allowed("customers") && customersAttention.isPending) ||
    (allowed("communications") && communicationsAttention.isPending) ||
    (allowed("products") && productsAttention.isPending) ||
    (allowed("energy") && energyAttention.isPending) ||
    (allowed("projects") && projectsAttention.isPending) ||
    (allowed("time") && timeAttention.isPending) ||
    (allowed("expenses") && expensesAttention.isPending);
  const activityLoading =
    (allowed("customers") && customersTimeseries.isPending) ||
    (allowed("communications") && communicationsTimeseries.isPending) ||
    (allowed("products") && productsTimeseries.isPending) ||
    (allowed("energy") && energyTimeseries.isPending) ||
    (allowed("projects") && projectsTimeseries.isPending) ||
    (allowed("time") && timeTimeseries.isPending) ||
    (allowed("expenses") && expensesTimeseries.isPending);

  const attentionItems = [
    ...(customersAttention.data ?? []).map((item) => ({ ...item, module: "customers" as ModuleKey })),
    ...(communicationsAttention.data ?? []).map((item) => ({ ...item, module: "communications" as ModuleKey })),
    ...(productsAttention.data ?? []).map((item) => ({ ...item, module: "products" as ModuleKey })),
    ...(energyAttention.data ?? []).map((item) => ({ ...item, module: "energy" as ModuleKey })),
    ...(projectsAttention.data ?? []).map((item) => ({ ...item, module: "projects" as ModuleKey })),
    ...(timeAttention.data ?? []).map((item) => ({ ...item, module: "time" as ModuleKey })),
    ...(expensesAttention.data ?? []).map((item) => ({ ...item, module: "expenses" as ModuleKey })),
  ]
    .sort((a, b) => new Date(b.occurredAt).getTime() - new Date(a.occurredAt).getTime())
    .slice(0, 8);
  const activity = [
    ...(customersTimeseries.data ?? [])
      .slice(-3)
      .map((item) => ({ ...item, module: "customers" as ModuleKey, metric: "newCustomers" })),
    ...(communicationsTimeseries.data ?? [])
      .slice(-3)
      .map((item) => ({ ...item, module: "communications" as ModuleKey, metric: "newConversations" })),
    ...(productsTimeseries.data ?? [])
      .slice(-3)
      .map((item) => ({ ...item, module: "products" as ModuleKey, metric: "newProducts" })),
    ...(energyTimeseries.data ?? [])
      .slice(-3)
      .map((item) => ({ ...item, module: "energy" as ModuleKey, metric: "consumptionKwh" })),
    ...(projectsTimeseries.data ?? [])
      .slice(-3)
      .map((item) => ({ ...item, module: "projects" as ModuleKey, metric: "newProjects" })),
    ...(timeTimeseries.data ?? []).slice(-3).map((item) => ({ ...item, module: "time" as ModuleKey, metric: "hours" })),
    ...(expensesTimeseries.data ?? [])
      .slice(-3)
      .map((item) => ({ ...item, module: "expenses" as ModuleKey, metric: "netAmount" })),
  ]
    .sort((a, b) => new Date(b.date).getTime() - new Date(a.date).getTime())
    .slice(0, 6);

  const setupItems = [
    {
      module: "customers" as ModuleKey,
      label: t("dashboard.addFirstCustomer"),
      href: "/customers",
      complete: (customersSummary.data?.totalActiveCustomers ?? 0) > 0,
      loading: customersSummary.isPending,
    },
    {
      module: "communications" as ModuleKey,
      label: t("dashboard.connectChannel"),
      href: "/communications/inbox",
      complete: (communicationsSummary.data?.newConversations ?? 0) > 0,
      loading: communicationsSummary.isPending,
    },
    {
      module: "products" as ModuleKey,
      label: t("dashboard.createProduct"),
      href: "/products",
      complete: (productsSummary.data?.totalActiveProducts ?? 0) > 0,
      loading: productsSummary.isPending,
    },
    {
      module: "energy" as ModuleKey,
      label: t("dashboard.configureMetering"),
      href: "/energy/metering-points",
      complete: (energySummary.data?.meteringPointCount ?? 0) > 0,
      loading: energySummary.isPending,
    },
    {
      module: "projects" as ModuleKey,
      label: t("dashboard.createFirstProject"),
      href: "/projects",
      complete: (projectsSummary.data?.activeProjects ?? 0) > 0,
      loading: projectsSummary.isPending,
    },
  ].filter((item) => allowed(item.module));
  const incompleteSetupItems = setupItems.filter((item) => !item.complete);
  const freshness = `${formatters.formatDate(range.from, { dateStyle: "medium" })} – ${formatters.formatDate(range.to, { dateStyle: "medium" })}`;
  const isSettled = !authorization.isPending;

  const updatePreset = (value: string) => {
    const preset = value as DashboardPreset;
    void navigate({ search: (current) => ({ ...current, preset, from: undefined, to: undefined }) });
  };

  const updateCustomRange = (value: [string | null, string | null]) => {
    void navigate({
      search: (current) => ({
        ...current,
        preset: "custom" as const,
        from: value[0] ? dateOnly(new Date(value[0])) : undefined,
        to: value[1] ? dateOnly(new Date(value[1])) : undefined,
      }),
    });
  };

  return (
    <Stack gap="xl">
      <PageHeader
        title={t("dashboard.title")}
        description={t("dashboard.greeting", { name: session?.user.displayName ?? "" })}
        actions={
          <Group align="flex-end" gap="sm">
            <SegmentedControl
              value={search.preset}
              onChange={updatePreset}
              data={[
                { value: "7d", label: t("dashboard.7d") },
                { value: "30d", label: t("dashboard.30d") },
                { value: "90d", label: t("dashboard.90d") },
                { value: "12m", label: t("dashboard.12m") },
                { value: "custom", label: t("dashboard.custom") },
              ]}
            />
            {search.preset === "custom" && (
              <DatePickerInput
                type="range"
                aria-label={t("dashboard.dateRange")}
                value={[
                  search.from ? new Date(`${search.from}T00:00:00`) : null,
                  search.to ? new Date(`${search.to}T00:00:00`) : null,
                ]}
                onChange={(value) => updateCustomRange(value)}
                valueFormat="YYYY-MM-DD"
                placeholder={t("dashboard.selectDateRange")}
                clearable
              />
            )}
          </Group>
        }
      />

      {/* One column per card up to four; a fifth module wraps rather than
          squeezing every card past reading width. */}
      <SimpleGrid cols={{ base: 1, xs: 2, md: Math.min(modules.length, 4) || 1 }} spacing="md">
        {modules.map((module) => {
          const href = module.path;
          if (module.module === "customers") {
            return (
              <KpiCard
                key={module.module}
                label={t("dashboard.newCustomers")}
                value={customersSummary.data?.newCustomers ?? "—"}
                hint={t("dashboard.customersKpiHint")}
                delta={
                  customersSummary.data
                    ? {
                        value: deltaPercent(
                          customersSummary.data.newCustomers,
                          customersSummary.data.newCustomersDelta,
                        ),
                        label: t("dashboard.vsPrevious"),
                      }
                    : undefined
                }
                sparklineData={sparkline(customersTimeseries.data)}
                href={href}
                loading={customersSummary.isPending || customersTimeseries.isPending}
              />
            );
          }
          if (module.module === "communications") {
            return (
              <KpiCard
                key={module.module}
                label={t("dashboard.openConversations")}
                value={communicationsSummary.data?.openConversations ?? "—"}
                hint={t("dashboard.newConversationsHint", { count: communicationsSummary.data?.newConversations ?? 0 })}
                delta={
                  communicationsSummary.data
                    ? {
                        value: deltaPercent(
                          communicationsSummary.data.newConversations,
                          communicationsSummary.data.newConversationsDelta,
                        ),
                        label: t("dashboard.vsPrevious"),
                      }
                    : undefined
                }
                sparklineData={sparkline(communicationsTimeseries.data)}
                href={href}
                loading={communicationsSummary.isPending || communicationsTimeseries.isPending}
              />
            );
          }
          if (module.module === "projects") {
            return (
              <KpiCard
                key={module.module}
                label={t("dashboard.activeProjects")}
                value={projectsSummary.data?.activeProjects ?? "—"}
                // Ready milestones are the more actionable figure, so they
                // take the hint when there are any; otherwise the card falls
                // back to the "N new projects" hint it always showed.
                hint={projectsCardHint(projectsSummary.data?.readyMilestones, projectsSummary.data?.newProjects, t)}
                delta={
                  projectsSummary.data
                    ? {
                        value: deltaPercent(projectsSummary.data.newProjects, projectsSummary.data.newProjectsDelta),
                        label: t("dashboard.vsPrevious"),
                      }
                    : undefined
                }
                sparklineData={sparkline(projectsTimeseries.data)}
                href={href}
                loading={projectsSummary.isPending || projectsTimeseries.isPending}
              />
            );
          }
          if (module.module === "time") {
            return (
              <KpiCard
                key={module.module}
                label={t("dashboard.hoursThisWeek")}
                value={
                  timeSummary.data
                    ? formatters.formatNumber(timeSummary.data.hoursThisWeek, { maximumFractionDigits: 2 })
                    : "—"
                }
                // The second figure is the one that asks for an action, so it
                // is the hint rather than a card of its own — but only while
                // there is something in it to act on.
                hint={awaitingApprovalHint(timeSummary.data?.awaitingMyApproval, t)}
                delta={
                  timeSummary.data
                    ? {
                        value: deltaPercent(timeSummary.data.hoursThisWeek, timeSummary.data.hoursThisWeekDelta),
                        label: t("dashboard.vsPrevious"),
                      }
                    : undefined
                }
                sparklineData={sparkline(timeTimeseries.data)}
                href={href}
                loading={timeSummary.isPending || timeTimeseries.isPending}
              />
            );
          }
          if (module.module === "expenses") {
            return (
              <KpiCard
                key={module.module}
                label={t("dashboard.unreimbursed")}
                value={
                  expensesSummary.data
                    ? expensesUnreimbursedValue(expensesSummary.data.myUnreimbursed, formatters.formatCurrency, t)
                    : "—"
                }
                // The more actionable figure — approvals waiting on this
                // caller — is the hint rather than a card of its own, and it
                // is hidden once there is nothing to approve, the same rule
                // as Time's own approval hint.
                hint={awaitingApprovalHint(expensesSummary.data?.awaitingMyApproval, t)}
                sparklineData={sparkline(expensesTimeseries.data)}
                href={href}
                loading={expensesSummary.isPending || expensesTimeseries.isPending}
              />
            );
          }
          if (module.module === "products") {
            return (
              <KpiCard
                key={module.module}
                label={t("dashboard.activeProducts")}
                value={productsSummary.data?.totalActiveProducts ?? "—"}
                hint={t("dashboard.newProductsHint", { count: productsSummary.data?.newProducts ?? 0 })}
                delta={
                  productsSummary.data
                    ? {
                        value: deltaPercent(productsSummary.data.newProducts, productsSummary.data.newProductsDelta),
                        label: t("dashboard.vsPrevious"),
                      }
                    : undefined
                }
                sparklineData={sparkline(productsTimeseries.data)}
                href={href}
                loading={productsSummary.isPending || productsTimeseries.isPending}
              />
            );
          }
          return (
            <KpiCard
              key={module.module}
              label={t("dashboard.consumption")}
              value={
                energySummary.data
                  ? `${formatters.formatNumber(energySummary.data.consumptionKwh, { maximumFractionDigits: 1 })} kWh`
                  : "—"
              }
              hint={t("dashboard.energyKpiHint")}
              delta={
                energySummary.data
                  ? {
                      value: deltaPercent(
                        energySummary.data.previousConsumptionKwh + energySummary.data.consumptionKwhDelta,
                        energySummary.data.consumptionKwhDelta,
                      ),
                      label: t("dashboard.vsPrevious"),
                    }
                  : undefined
              }
              sparklineData={sparkline(energyTimeseries.data)}
              href={href}
              loading={energySummary.isPending || energyTimeseries.isPending}
            />
          );
        })}
      </SimpleGrid>

      {incompleteSetupItems.length > 0 && (
        <WidgetCard
          title={t("dashboard.setup")}
          description={t("dashboard.setupDescription")}
          loading={setupItems.some((item) => item.loading)}
        >
          <Group mt="lg" align="center" wrap="nowrap">
            <RingProgress
              size={92}
              thickness={9}
              sections={[
                {
                  value: ((setupItems.length - incompleteSetupItems.length) / Math.max(setupItems.length, 1)) * 100,
                  color: "indigo.6",
                },
              ]}
              label={
                <Text ta="center" fw={700}>
                  {setupItems.length - incompleteSetupItems.length}/{setupItems.length}
                </Text>
              }
            />
            <Stack gap="xs">
              {incompleteSetupItems.map((item) => (
                <Anchor key={item.module} component={Link} to={item.href} c="inherit" size="sm">
                  <Group gap="xs" wrap="nowrap">
                    <IconChecklist size={16} />
                    <Text>{item.label}</Text>
                  </Group>
                </Anchor>
              ))}
            </Stack>
          </Group>
        </WidgetCard>
      )}

      <Grid gap="md">
        <Grid.Col span={{ base: 12, md: 8 }}>
          <WidgetCard
            title={t("dashboard.trend")}
            description={selectedMetric ? t(selectedMetric.label) : t("dashboard.noTrendData")}
            freshness={freshness}
            action={
              availableMetrics.length > 0 ? (
                <Select
                  aria-label={t("dashboard.selectMetric")}
                  size="xs"
                  w={180}
                  value={selectedMetricValue}
                  onChange={(value) => {
                    if (value) void navigate({ search: (current) => ({ ...current, metric: value }) });
                  }}
                  data={availableMetrics.map((metric) => ({
                    value: `${metric.module}:${metric.metric}`,
                    label: t(metric.label),
                  }))}
                />
              ) : undefined
            }
            loading={selectedTimeseriesQuery?.isPending}
            empty={!selectedTimeseriesQuery?.isPending && (!selectedTimeseries || selectedTimeseries.length === 0)}
            emptyState={{
              message: t("dashboard.noTrendData"),
              action: selectedMetric ? (
                <Anchor
                  component={Link}
                  to={moduleCards.find((item) => item.module === selectedMetric.module)?.path ?? "/"}
                >
                  {t("dashboard.openModule")}
                </Anchor>
              ) : undefined,
            }}
          >
            <AreaChart
              h={280}
              mt="lg"
              data={(selectedTimeseries ?? ([] as DailyPoint[])).map((point: DailyPoint) => ({
                date: formatters.formatDate(point.date, { month: "short", day: "numeric" }),
                value: point.value,
              }))}
              dataKey="date"
              series={[
                {
                  name: "value",
                  label: selectedMetric ? t(selectedMetric.label) : "",
                  color: selectedMetric?.color ?? "blue.6",
                },
              ]}
              curveType="natural"
              withDots
            />
          </WidgetCard>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 4 }}>
          <WidgetCard
            title={t("dashboard.needsAttention")}
            description={t("dashboard.needsAttentionDescription")}
            loading={attentionLoading}
            empty={!attentionLoading && attentionItems.length === 0}
            emptyState={{
              message: (
                <Group justify="center" gap="xs">
                  <IconCircleCheck size={20} />
                  <Text>{t("dashboard.allClear")}</Text>
                </Group>
              ),
            }}
          >
            <Stack gap="xs" mt="lg">
              {attentionItems.map((item) => {
                const Icon =
                  item.type === "supplyPeriodExpiring" || item.module === "time"
                    ? IconClock
                    : item.type === "failedDelivery"
                      ? IconRefreshAlert
                      : IconAlertCircle;
                // Every other module writes its own title; time's is named
                // here instead, so it arrives in the reader's language.
                const title = attentionTitle(item, t, formatters.formatDate);
                return (
                  <Anchor
                    key={`${item.module}-${item.id}`}
                    component={Link}
                    to={attentionHref(item)}
                    c="inherit"
                    underline="never"
                  >
                    <Group gap="sm" wrap="nowrap">
                      <ThemeIcon variant="light" color={item.type === "failedDelivery" ? "red" : "yellow"} size="sm">
                        <Icon size={14} />
                      </ThemeIcon>
                      <Stack gap={0} style={{ minWidth: 0 }} flex={1}>
                        <Text size="sm" truncate>
                          {title}
                        </Text>
                        <Text size="xs" c="dimmed">
                          {relativeTime(item.occurredAt, locale)}
                        </Text>
                      </Stack>
                    </Group>
                  </Anchor>
                );
              })}
            </Stack>
          </WidgetCard>
        </Grid.Col>

        {allowed("communications") && (
          <Grid.Col span={{ base: 12, md: 4 }}>
            <WidgetCard
              title={t("dashboard.conversationStatus")}
              loading={communicationsSummary.isPending}
              empty={!communicationsSummary.isPending && !communicationsSummary.data}
              emptyState={{
                message: t("dashboard.noWidgetData"),
                action: (
                  <Anchor component={Link} to="/communications/inbox">
                    {t("dashboard.openModule")}
                  </Anchor>
                ),
              }}
            >
              <DonutChart
                mt="lg"
                data={
                  communicationsSummary.data
                    ? [
                        {
                          name: t("dashboard.open"),
                          value: communicationsSummary.data.openConversations,
                          color: "blue.6",
                        },
                        {
                          name: t("dashboard.closed"),
                          value: communicationsSummary.data.closedConversations,
                          color: "gray.6",
                        },
                        {
                          name: t("dashboard.new"),
                          value: communicationsSummary.data.newConversations,
                          color: "violet.6",
                        },
                      ]
                    : []
                }
                size={190}
                thickness={28}
                withLabels
                withTooltip
              />
            </WidgetCard>
          </Grid.Col>
        )}

        {allowed("products") && (
          <Grid.Col span={{ base: 12, md: 4 }}>
            <WidgetCard
              title={t("dashboard.productStatus")}
              loading={productsSummary.isPending || productsTimeseries.isPending}
              empty={!productsSummary.isPending && !productsTimeseries.isPending && !productsSummary.data}
              emptyState={{
                message: t("dashboard.noWidgetData"),
                action: (
                  <Anchor component={Link} to="/products">
                    {t("dashboard.openModule")}
                  </Anchor>
                ),
              }}
            >
              <BarChart
                mt="lg"
                h={220}
                data={
                  productsSummary.data
                    ? Object.entries(productsSummary.data.statusCounts).map(([status, value]) => ({ status, value }))
                    : []
                }
                dataKey="status"
                series={[{ name: "value", label: t("dashboard.products"), color: "orange.6" }]}
                withTooltip
              />
            </WidgetCard>
          </Grid.Col>
        )}

        <Grid.Col span={{ base: 12, md: allowed("communications") || allowed("products") ? 4 : 12 }}>
          <WidgetCard
            title={t("dashboard.activity")}
            description={t("dashboard.activityDescription")}
            loading={activityLoading}
            empty={!activityLoading && activity.length === 0}
            emptyState={{
              message: t("dashboard.noActivity"),
              action: modules[0] ? (
                <Anchor component={Link} to={modules[0].path}>
                  {t("dashboard.openModule")}
                </Anchor>
              ) : undefined,
            }}
          >
            <Stack gap="xs" mt="lg">
              {activity.map((item) => (
                <Group key={`${item.module}-${item.date}-${item.metric}`} justify="space-between" wrap="nowrap">
                  <Group gap="xs" wrap="nowrap">
                    <IconInbox size={15} />
                    <Text size="sm">
                      {t(metrics.find((metric) => metric.module === item.module)?.label ?? "dashboard.activity")}
                    </Text>
                  </Group>
                  <Text size="sm" fw={600}>
                    {formatters.formatNumber(item.value, { maximumFractionDigits: 1 })}
                  </Text>
                </Group>
              ))}
            </Stack>
          </WidgetCard>
        </Grid.Col>
      </Grid>

      {isSettled && modules.length === 0 && (
        <Alert color="gray" variant="light" title={t("noAccessTitle")}>
          {t("noAccessBody")}
        </Alert>
      )}

      {modules.length > 0 && (
        <Stack gap="sm">
          <Text fw={600}>{t("dashboard.modules")}</Text>
          <SimpleGrid cols={{ base: 1, xs: 2, md: Math.min(modules.length, 4) || 1 }} spacing="sm">
            {modules.map((module) => (
              <Card key={module.path} withBorder padding="sm">
                <Group justify="space-between" wrap="nowrap">
                  <Group gap="xs" wrap="nowrap">
                    <module.icon size={18} />
                    <Text size="sm" fw={600}>
                      {t(module.title)}
                    </Text>
                  </Group>
                  <Anchor component={Link} to={module.path} size="sm">
                    {t("dashboard.open")}
                  </Anchor>
                </Group>
              </Card>
            ))}
          </SimpleGrid>
        </Stack>
      )}
    </Stack>
  );
};

export const Route = createFileRoute("/dashboard")({
  staticData: { app: "home" },
  validateSearch: (search: Record<string, unknown>) => {
    const rawPreset = search.preset;
    const hasCustomDates = typeof search.from === "string" && typeof search.to === "string";
    const preset: DashboardPreset = ["7d", "30d", "90d", "12m", "custom"].includes(String(rawPreset))
      ? (String(rawPreset) as DashboardPreset)
      : hasCustomDates
        ? "custom"
        : DEFAULT_DASHBOARD_PRESET;
    return {
      preset,
      from: typeof search.from === "string" ? search.from : undefined,
      to: typeof search.to === "string" ? search.to : undefined,
      metric: typeof search.metric === "string" ? search.metric : undefined,
    };
  },
  component: DashboardPage,
});
