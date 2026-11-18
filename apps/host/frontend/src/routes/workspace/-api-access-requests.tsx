import { BarChart } from "@mantine/charts";
import { Alert, Badge, Code, Group, Select, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageSection, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  type ApiKey,
  getIntegrationRequests,
  type IntegrationRequestLog,
  integrationRequestsQueryKey,
  type RequestLogFilters,
} from "../../api/integrations";

/** The status classes the log counts, and the reasons a refusal is recorded under (K23, K24). */
export const statusClasses = [2, 3, 4, 5] as const;
export const refusalReasons = [
  "expired",
  "revoked",
  "disabled",
  "archived",
  "network",
  "read_only",
  "never",
  "forbidden",
  "rate_limited",
] as const;

/**
 * One color per status class, in a fixed order. The four were checked
 * together, against the light and the dark surface, for lightness, chroma,
 * colour-blind and normal-vision separation and contrast; red is left for
 * the alerts.
 */
const classColor: Record<(typeof statusClasses)[number], string> = {
  2: "teal.7",
  3: "blue.7",
  4: "orange.8",
  5: "grape.7",
};

/** The UTC days of the last 30, newest first, as yyyy-mm-dd. */
const lastDays = (now: Date) =>
  Array.from({ length: 30 }, (_, i) => {
    const day = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() - i));
    return day.toISOString().slice(0, 10);
  });

/** The chart's rows: one per day, oldest first, with a count per status class. */
export const dayRows = (log: IntegrationRequestLog) => {
  const byDay = new Map<string, Record<string, number | string>>();
  for (const entry of log.days) {
    const row = byDay.get(entry.day) ?? { day: entry.day };
    row[`class${entry.statusClass}`] = entry.requests;
    byDay.set(entry.day, row);
  }
  return [...byDay.values()].sort((a, b) => String(a.day).localeCompare(String(b.day)));
};

/**
 * The integration's Requests tab (API access design K23): its requests per
 * UTC day by status class, the operations called most and the latest 200,
 * filtered by key, status class, reason and day. Nothing in it is a body, a
 * query string or a path value: the log holds none.
 */
export const IntegrationRequests = ({ integrationId, keys }: { integrationId: string; keys: ApiKey[] }) => {
  const { t, formatters } = useI18n("host");
  const [filters, setFilters] = useState<RequestLogFilters>({});
  const log = useQuery({
    queryKey: integrationRequestsQueryKey(integrationId, filters),
    queryFn: () => getIntegrationRequests(integrationId, filters),
    placeholderData: keepPreviousData,
  });
  const set = (patch: RequestLogFilters) => setFilters((current) => ({ ...current, ...patch }));
  const classLabel = (statusClass: number) => t(`apiAccess.statusClass_${statusClass}`);

  const filterRow = (
    <Group align="flex-end" gap="sm" data-testid="request-filters">
      <Select
        label={t("apiAccess.filterKey")}
        placeholder={t("apiAccess.filterAllKeys")}
        clearable
        data={keys.map((key) => ({ value: key.id, label: key.name }))}
        value={filters.key ?? null}
        onChange={(value) => set({ key: value ?? undefined })}
      />
      <Select
        label={t("apiAccess.filterStatus")}
        placeholder={t("apiAccess.filterAllStatuses")}
        clearable
        data={statusClasses.map((value) => ({ value: String(value), label: classLabel(value) }))}
        value={filters.statusClass === undefined ? null : String(filters.statusClass)}
        onChange={(value) => set({ statusClass: value ? Number(value) : undefined })}
      />
      <Select
        label={t("apiAccess.filterReason")}
        placeholder={t("apiAccess.filterAllReasons")}
        clearable
        data={refusalReasons.map((value) => ({ value, label: t(`apiAccess.reason_${value}`) }))}
        value={filters.reason ?? null}
        onChange={(value) => set({ reason: value ?? undefined })}
      />
      <Select
        label={t("apiAccess.filterDay")}
        placeholder={t("apiAccess.filterAllDays")}
        clearable
        data={lastDays(new Date()).map((value) => ({
          value,
          label: formatters.formatDate(`${value}T00:00:00Z`, { dateStyle: "medium", timeZone: "UTC" }),
        }))}
        value={filters.day ?? null}
        onChange={(value) => set({ day: value ?? undefined })}
      />
    </Group>
  );

  // The filters stay where they are while the log loads, so a choice is
  // never lost to a re-render; the previous answer shows until the next one.
  let body: ReactNode;
  if (log.isPending) {
    body = <ContentSkeleton />;
  } else if (log.isError) {
    body = <Alert color="red" title={t("apiAccess.couldNotLoadRequests")} />;
  } else {
    body = <RequestLog log={log.data} keyName={new Map(keys.map((key) => [key.id, key.name]))} />;
  }
  return (
    <Stack gap="xl">
      <Text c="dimmed" size="sm">
        {t("apiAccess.requestsHint")}
      </Text>
      {filterRow}
      {body}
    </Stack>
  );
};

/** The log under the filters: the chart and its table, the operations, the latest requests. */
const RequestLog = ({ log: data, keyName }: { log: IntegrationRequestLog; keyName: Map<string, string> }) => {
  const { t, formatters } = useI18n("host");
  const classLabel = (statusClass: number) => t(`apiAccess.statusClass_${statusClass}`);
  const rows = dayRows(data);
  return (
    <>
      {data.latest.length === 0 ? (
        <EmptyState title={t("apiAccess.noRequests")} description={t("apiAccess.noRequestsHint")} />
      ) : (
        <>
          <SimpleGrid cols={{ base: 1, md: 3 }} spacing="xl">
            <PageSection title={t("apiAccess.requestsPerDay")} style={{ gridColumn: "span 2" }}>
              <Stack gap="sm">
                <Text c="dimmed" size="sm">
                  {t("apiAccess.requestsPerDayHint")}
                </Text>
                <BarChart
                  h={240}
                  data={rows}
                  dataKey="day"
                  type="stacked"
                  withLegend
                  withTooltip
                  tooltipAnimationDuration={0}
                  series={statusClasses.map((statusClass) => ({
                    name: `class${statusClass}`,
                    label: classLabel(statusClass),
                    color: classColor[statusClass],
                  }))}
                />
                <Table data-testid="request-days" fz="sm">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("apiAccess.requestDay")}</Table.Th>
                      {statusClasses.map((statusClass) => (
                        <Table.Th key={statusClass} ta="right">
                          {classLabel(statusClass)}
                        </Table.Th>
                      ))}
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {rows.map((row) => (
                      <Table.Tr key={String(row.day)}>
                        <Table.Td>{String(row.day)}</Table.Td>
                        {statusClasses.map((statusClass) => (
                          <Table.Td key={statusClass} ta="right">
                            {formatters.formatNumber(Number(row[`class${statusClass}`] ?? 0))}
                          </Table.Td>
                        ))}
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Stack>
            </PageSection>
            <PageSection title={t("apiAccess.topOperations")}>
              <Table data-testid="request-operations" fz="sm">
                <Table.Tbody>
                  {data.operations.map((operation) => (
                    <Table.Tr key={operation.operationId}>
                      <Table.Td>
                        <Code>{operation.operationId}</Code>
                      </Table.Td>
                      <Table.Td ta="right">{formatters.formatNumber(operation.requests)}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </PageSection>
          </SimpleGrid>

          <PageSection title={t("apiAccess.latestRequests")}>
            <Table.ScrollContainer minWidth={900}>
              <Table data-testid="request-latest" fz="sm" verticalSpacing="xs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("apiAccess.requestAt")}</Table.Th>
                    <Table.Th>{t("apiAccess.requestKey")}</Table.Th>
                    <Table.Th>{t("apiAccess.requestOperation")}</Table.Th>
                    <Table.Th>{t("apiAccess.requestStatus")}</Table.Th>
                    <Table.Th ta="right">{t("apiAccess.requestDuration")}</Table.Th>
                    <Table.Th>{t("apiAccess.requestClient")}</Table.Th>
                    <Table.Th>{t("apiAccess.requestTrace")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {data.latest.map((request, index) => (
                    <Table.Tr key={`${request.at}-${request.keyId}-${index}`}>
                      <Table.Td>
                        {formatters.formatDate(request.at, { dateStyle: "short", timeStyle: "medium" })}
                      </Table.Td>
                      <Table.Td>
                        {request.keyName ?? keyName.get(request.keyId) ?? (
                          <Text span c="dimmed" size="sm">
                            {t("apiAccess.deletedKey")}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td>
                        <Text span size="sm">
                          {request.method}{" "}
                        </Text>
                        <Code>{request.operationId}</Code>
                      </Table.Td>
                      <Table.Td>
                        <Group gap={4} wrap="nowrap">
                          <Text span size="sm" fw={600}>
                            {request.status}
                          </Text>
                          {request.reason && (
                            <Badge variant="light" color="gray" size="sm">
                              {t(`apiAccess.reason_${request.reason}`)}
                            </Badge>
                          )}
                        </Group>
                      </Table.Td>
                      <Table.Td ta="right">{t("apiAccess.durationMs", { ms: request.durationMs })}</Table.Td>
                      <Table.Td>{request.clientIp ?? t("apiAccess.none")}</Table.Td>
                      <Table.Td>{request.traceId ? <Code>{request.traceId}</Code> : t("apiAccess.none")}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </PageSection>
        </>
      )}
    </>
  );
};
