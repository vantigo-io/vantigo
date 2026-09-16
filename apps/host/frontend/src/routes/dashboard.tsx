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
  Title,
} from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import {
  IconAlertCircle,
  IconBolt,
  IconChecklist,
  IconCircleCheck,
  IconClock,
  IconInbox,
  IconMessage,
  IconPackage,
  IconRefreshAlert,
  IconUsers,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
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

interface AttentionItem {
  id: string;
  type: string;
  title: string;
  occurredAt: string;
  entityId: string;
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
    path: "/inbox",
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

  const timeseriesByMetric: Record<string, DailyPoint[] | undefined> = {
    "customers:newCustomers": customersTimeseries.data,
    "communications:newConversations": communicationsTimeseries.data,
    "products:newProducts": productsTimeseries.data,
    "energy:consumptionKwh": energyTimeseries.data,
  };
  const timeseriesQueries: Record<string, { isPending: boolean; isError: boolean }> = {
    "customers:newCustomers": customersTimeseries,
    "communications:newConversations": communicationsTimeseries,
    "products:newProducts": productsTimeseries,
    "energy:consumptionKwh": energyTimeseries,
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
    (allowed("energy") && energyAttention.isPending);
  const activityLoading =
    (allowed("customers") && customersTimeseries.isPending) ||
    (allowed("communications") && communicationsTimeseries.isPending) ||
    (allowed("products") && productsTimeseries.isPending) ||
    (allowed("energy") && energyTimeseries.isPending);

  const attentionItems = [
    ...(customersAttention.data ?? []).map((item) => ({ ...item, module: "customers" as ModuleKey })),
    ...(communicationsAttention.data ?? []).map((item) => ({ ...item, module: "communications" as ModuleKey })),
    ...(productsAttention.data ?? []).map((item) => ({ ...item, module: "products" as ModuleKey })),
    ...(energyAttention.data ?? []).map((item) => ({ ...item, module: "energy" as ModuleKey })),
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
  ]
    .sort((a, b) => new Date(b.date).getTime() - new Date(a.date).getTime())
    .slice(0, 6);

  const attentionHref = (item: (typeof attentionItems)[number]) => {
    if (item.module === "communications" && item.type === "conversationNoReply") {
      return `/inbox?conversationId=${encodeURIComponent(item.entityId)}`;
    }
    if (item.module === "customers") return `/customers/${encodeURIComponent(item.entityId)}`;
    if (item.module === "products") return `/products/${encodeURIComponent(item.entityId)}`;
    if (item.module === "energy") return `/energy/metering-points/${encodeURIComponent(item.entityId)}`;
    return "/inbox";
  };
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
      href: "/inbox",
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
      <Group justify="space-between" align="flex-end" wrap="wrap" gap="md">
        <Stack gap={2}>
          <Text c="dimmed">{t("dashboard.greeting", { name: session?.user.displayName ?? "" })}</Text>
          <Title order={2}>{t("dashboard.title")}</Title>
        </Stack>
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
      </Group>

      <SimpleGrid cols={{ base: 1, xs: 2, md: modules.length || 1 }} spacing="md">
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
                  item.type === "supplyPeriodExpiring"
                    ? IconClock
                    : item.type === "failedDelivery"
                      ? IconRefreshAlert
                      : IconAlertCircle;
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
                          {item.title}
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
                  <Anchor component={Link} to="/inbox">
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
          <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }} spacing="sm">
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
