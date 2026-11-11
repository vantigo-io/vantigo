import { Alert, Anchor, Button, Group, Select, Stack, Table, Tabs, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDownload } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { Fragment, useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import {
  downloadReportCsv,
  type ManufacturingProductionCostReport,
  type ManufacturingTimeDeviationReport,
  type ManufacturingUtilisationReport,
  type ReportFilter,
  type ReportKind,
  reportQueryOptions,
  saveCsv,
} from "../api/reports";
import { manufacturingSitesQueryOptions } from "../api/sites";
import { canSeeCosts } from "../lib/capabilities";
import { refusalMessage, useManufacturingFormat } from "../lib/format";
import { MANUFACTURING_HOME_ROUTE_PATH, orderLinkOptions } from "../lib/routes";

const tabLabel: Record<ReportKind, string> = {
  "production-cost": "reportProductionCost",
  utilisation: "reportUtilisation",
  "time-deviation": "reportTimeDeviation",
};

/** A per cent as the server rounded it, or a dash when there is none. */
const usePercent = () => {
  const { decimal } = useManufacturingFormat();
  return (text: string | null) => (text === null ? "—" : `${decimal(text)} %`);
};

const ProductionCost = ({ report, currency }: { report: ManufacturingProductionCostReport; currency: string }) => {
  const { t, money, quantity, stamp } = useManufacturingFormat();
  const percent = usePercent();
  const amount = (text: string | null) => (text === null ? "—" : money(text, currency));
  if (report.groups.length === 0) return <Empty />;
  return (
    <Table.ScrollContainer minWidth={960}>
      <Table>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("orderNumber")}</Table.Th>
            <Table.Th>{t("closedAt")}</Table.Th>
            <Table.Th ta="right">{t("quantity")}</Table.Th>
            <Table.Th ta="right">{t("produced")}</Table.Th>
            <Table.Th ta="right">{t("costEstimate")}</Table.Th>
            <Table.Th ta="right">{t("costActual")}</Table.Th>
            <Table.Th ta="right">{t("costDeviation")}</Table.Th>
            <Table.Th ta="right">{t("costDeviationPercent")}</Table.Th>
            <Table.Th ta="right">{t("costPerUnit")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {report.groups.map((group) => (
            <Fragment key={group.variant.id}>
              <Table.Tr bg="var(--mantine-color-default-hover)" fw={600}>
                <Table.Td colSpan={3}>
                  {`${group.variant.sku} · ${group.variant.productName} · ${t("reportOrders", { count: group.totals.orders })}`}
                </Table.Td>
                <Table.Td ta="right">{quantity(group.totals.produced, group.variant.unit)}</Table.Td>
                <Table.Td ta="right">{amount(group.totals.estimate)}</Table.Td>
                <Table.Td ta="right">{amount(group.totals.actual)}</Table.Td>
                <Table.Td ta="right">{amount(group.totals.deviation)}</Table.Td>
                <Table.Td ta="right">{percent(group.totals.deviationPercent)}</Table.Td>
                <Table.Td ta="right">{amount(group.totals.costPerUnit)}</Table.Td>
              </Table.Tr>
              {group.orders.map((row) => (
                <Table.Tr key={row.orderId}>
                  <Table.Td>
                    <Anchor size="sm" renderRoot={(props) => <Link {...orderLinkOptions(row.orderId)} {...props} />}>
                      {row.number}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>{stamp(row.closedAt)}</Table.Td>
                  <Table.Td ta="right">{quantity(row.quantity, group.variant.unit)}</Table.Td>
                  <Table.Td ta="right">{quantity(row.produced, group.variant.unit)}</Table.Td>
                  <Table.Td ta="right">{amount(row.estimate)}</Table.Td>
                  <Table.Td ta="right">{amount(row.actual)}</Table.Td>
                  <Table.Td ta="right">{amount(row.deviation)}</Table.Td>
                  <Table.Td ta="right">{percent(row.deviationPercent)}</Table.Td>
                  <Table.Td ta="right">{amount(row.costPerUnit)}</Table.Td>
                </Table.Tr>
              ))}
            </Fragment>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

const Empty = () => {
  const { t } = useManufacturingFormat();
  return (
    <Text size="sm" c="dimmed">
      {t("reportEmpty")}
    </Text>
  );
};

const Utilisation = ({ report }: { report: ManufacturingUtilisationReport }) => {
  const { t, minutes } = useManufacturingFormat();
  const percent = usePercent();
  if (report.rows.length === 0) return <Empty />;
  return (
    <Table.ScrollContainer minWidth={900}>
      <Table>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("workCenter")}</Table.Th>
            <Table.Th>{t("reportWeek")}</Table.Th>
            <Table.Th ta="right">{t("reportCapacity")}</Table.Th>
            <Table.Th ta="right">{t("reportPlanned")}</Table.Th>
            <Table.Th ta="right">{t("reportProductive")}</Table.Th>
            <Table.Th ta="right">{t("reportUtilisationPercent")}</Table.Th>
            <Table.Th>{t("reportDowntime")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {report.rows.map((row) => (
            <Table.Tr key={`${row.workCenter.id}-${row.week}`}>
              <Table.Td>{`${row.workCenter.code} · ${row.workCenter.name}`}</Table.Td>
              <Table.Td>{row.week}</Table.Td>
              <Table.Td ta="right">{minutes(row.capacityMinutes)}</Table.Td>
              <Table.Td ta="right">{minutes(row.plannedMinutes)}</Table.Td>
              <Table.Td ta="right">{minutes(row.productiveMinutes)}</Table.Td>
              <Table.Td ta="right">{percent(row.utilisationPercent)}</Table.Td>
              <Table.Td>
                {row.downtimeMinutes === 0
                  ? "—"
                  : [
                      minutes(row.downtimeMinutes),
                      ...row.downtime.map((one) =>
                        t("downtimeEntry", { reason: t(`reason_${one.reason}`), minutes: minutes(one.minutes) }),
                      ),
                    ].join(" · ")}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

const TimeDeviation = ({ report }: { report: ManufacturingTimeDeviationReport }) => {
  const { t, decimal } = useManufacturingFormat();
  const percent = usePercent();
  const minutesText = (text: string) => t("minutesValue", { value: decimal(text) });
  if (report.rows.length === 0) return <Empty />;
  return (
    <Table.ScrollContainer minWidth={960}>
      <Table>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("product")}</Table.Th>
            <Table.Th>{t("bom")}</Table.Th>
            <Table.Th>{t("sequence")}</Table.Th>
            <Table.Th>{t("operationName")}</Table.Th>
            <Table.Th>{t("workCenter")}</Table.Th>
            <Table.Th ta="right">{t("reportWorkOrders")}</Table.Th>
            <Table.Th ta="right">{t("reportAverageExpected")}</Table.Th>
            <Table.Th ta="right">{t("reportAverageActual")}</Table.Th>
            <Table.Th ta="right">{t("costDeviationPercent")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {report.rows.map((row) => (
            <Table.Tr key={`${row.bomId}-${row.sequence}`}>
              <Table.Td>{`${row.variant.sku} · ${row.variant.productName}`}</Table.Td>
              <Table.Td>{row.bomCode ?? t("bomWithoutCode")}</Table.Td>
              <Table.Td>{row.sequence}</Table.Td>
              <Table.Td>{row.name}</Table.Td>
              <Table.Td>{row.workCenter.code}</Table.Td>
              <Table.Td ta="right">{row.workOrders}</Table.Td>
              <Table.Td ta="right">{minutesText(row.averageExpectedMinutes)}</Table.Td>
              <Table.Td ta="right">{minutesText(row.averageActualMinutes)}</Table.Td>
              <Table.Td ta="right">{percent(row.deviationPercent)}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

/** One report's answer for the filters, the period it covers and its CSV download. */
const Report = ({ kind, filter, currency }: { kind: ReportKind; filter: ReportFilter; currency: string }) => {
  const { t, day } = useManufacturingFormat();
  const report = useQuery(reportQueryOptions(kind, filter));
  const [downloading, setDownloading] = useState(false);

  const download = async () => {
    setDownloading(true);
    try {
      saveCsv(await downloadReportCsv(kind, filter));
    } catch (error) {
      notifications.show({ color: "red", title: t("couldNotDownload"), message: refusalMessage(t, error as Error) });
    } finally {
      setDownloading(false);
    }
  };

  return (
    <PageSection
      title={t(tabLabel[kind])}
      actions={
        <Button
          variant="default"
          leftSection={<IconDownload size={16} />}
          loading={downloading}
          data-assist="manufacturing.reports.download"
          onClick={() => void download()}
        >
          {t("downloadCsv")}
        </Button>
      }
    >
      {report.data && (
        <Text size="sm" c="dimmed" mb="sm">
          {t("reportPeriod", { from: day(report.data.from), to: day(report.data.to) })}
        </Text>
      )}
      {report.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {refusalMessage(t, report.error)}
        </Alert>
      ) : report.isPending ? (
        <ContentSkeleton rows={3} />
      ) : kind === "production-cost" ? (
        <ProductionCost report={report.data as ManufacturingProductionCostReport} currency={currency} />
      ) : kind === "utilisation" ? (
        <Utilisation report={report.data as ManufacturingUtilisationReport} />
      ) : (
        <TimeDeviation report={report.data as ManufacturingTimeDeviationReport} />
      )}
    </PageSection>
  );
};

/**
 * The reports (M42), under the overview: production cost — only for whoever
 * may see costs (M38) — work-center utilisation and time deviation, each over
 * a period (the last 30 days by default) and a site, with its CSV download.
 */
export const ReportsPage = () => {
  const { t } = useManufacturingFormat();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const sites = useQuery(manufacturingSitesQueryOptions());
  const [filter, setFilter] = useState<ReportFilter>({});
  const [tab, setTab] = useState<ReportKind | null>(null);
  const costs = canSeeCosts(meta.data);
  const kinds: ReportKind[] = costs
    ? ["production-cost", "utilisation", "time-deviation"]
    : ["utilisation", "time-deviation"];
  const active = tab && kinds.includes(tab) ? tab : kinds[0];

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
          { label: t("navOverview"), to: MANUFACTURING_HOME_ROUTE_PATH },
          { label: t("reports") },
        ]}
        title={t("reports")}
        description={t("reportsDescription")}
      />
      <Group gap="sm" align="flex-end" data-assist="manufacturing.reports.filters">
        <TextInput
          type="date"
          label={t("reportFrom")}
          value={filter.from ?? ""}
          onChange={(event) => {
            const value = event.currentTarget.value || undefined;
            setFilter((current) => ({ ...current, from: value }));
          }}
        />
        <TextInput
          type="date"
          label={t("reportTo")}
          value={filter.to ?? ""}
          onChange={(event) => {
            const value = event.currentTarget.value || undefined;
            setFilter((current) => ({ ...current, to: value }));
          }}
        />
        <Select
          label={t("site")}
          placeholder={t("allSites")}
          clearable
          w={220}
          data={(sites.data ?? []).map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }))}
          value={filter.siteId === undefined ? null : String(filter.siteId)}
          onChange={(next) => setFilter((current) => ({ ...current, siteId: next ? Number(next) : undefined }))}
        />
      </Group>
      {meta.isPending ? (
        <ContentSkeleton rows={3} />
      ) : (
        <Tabs value={active} onChange={(next) => setTab(next as ReportKind)} keepMounted={false}>
          <Tabs.List mb="md">
            {kinds.map((kind) => (
              <Tabs.Tab key={kind} value={kind}>
                {t(tabLabel[kind])}
              </Tabs.Tab>
            ))}
          </Tabs.List>
          {kinds.map((kind) => (
            <Tabs.Panel key={kind} value={kind}>
              <Report kind={kind} filter={filter} currency={meta.data?.currency ?? "NOK"} />
            </Tabs.Panel>
          ))}
        </Tabs>
      )}
    </Stack>
  );
};
