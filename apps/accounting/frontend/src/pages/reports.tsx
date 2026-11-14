import { Alert, Anchor, Button, Checkbox, Group, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { IconAlertCircle, IconDownload } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingMeta,
  type AccountingReport,
  type AccountingReportKind,
  type ReportParams,
  reportCsvUrl,
  reportKinds,
  reportQueryOptions,
} from "../api/accounting";
import { BooksGate } from "../components/books-gate";
import { AccountSelect, ProjectSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { fiscalYearStart, yearBefore } from "../lib/periods";
import { voucherPath } from "../lib/routes";

/** The reports with a comparison period (A11). */
const comparable: readonly AccountingReportKind[] = ["trial_balance", "profit_and_loss", "balance_sheet"];

/**
 * The specifications (A11) on one page: a picker, the period, a comparison
 * where the report has one, the account for the account report, a project,
 * and the CSV for those who may export.
 */
export const ReportsPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navReports")} description={t("reportsDescription")} />
          <Reports meta={meta} />
        </Stack>
      )}
    </BooksGate>
  );
};

const Reports = ({ meta }: { meta: AccountingMeta }) => {
  const { t } = useAccountingFormat();
  const [kind, setKind] = useState<AccountingReportKind>("trial_balance");
  const [from, setFrom] = useState(() => fiscalYearStart(meta.today, meta.settings?.fiscalYearStartMonth ?? 1));
  const [to, setTo] = useState(meta.today);
  const [compare, setCompare] = useState(false);
  const [account, setAccount] = useState<string | null>(null);
  const [project, setProject] = useState<string | null>(null);
  const params: ReportParams = {
    from,
    to,
    ...(compare && comparable.includes(kind) ? { compareFrom: yearBefore(from), compareTo: yearBefore(to) } : {}),
    ...(account && kind === "account" ? { accountId: Number(account) } : {}),
    ...(project ? { projectId: Number(project) } : {}),
  };
  const ready = from !== "" && to !== "" && (kind !== "account" || account !== null);
  const report = useQuery({ ...reportQueryOptions(kind, params), enabled: ready });
  return (
    <Stack gap="md">
      <Group align="flex-end" wrap="wrap" data-assist="accounting.reports.picker">
        <Select
          label={t("report")}
          data={reportKinds.map((k) => ({ value: k, label: t(`report_${k}`) }))}
          value={kind}
          onChange={(value) => value && setKind(value as AccountingReportKind)}
          allowDeselect={false}
        />
        <TextInput type="date" label={t("from")} value={from} onChange={(e) => setFrom(e.currentTarget.value)} />
        <TextInput type="date" label={t("to")} value={to} onChange={(e) => setTo(e.currentTarget.value)} />
        {kind === "account" && <AccountSelect label={t("account")} value={account} onChange={setAccount} miw={240} />}
        {meta.projectsEnabled && <ProjectSelect label={t("project")} value={project} onChange={setProject} />}
        {comparable.includes(kind) && (
          <Checkbox
            label={t("compareWithYearBefore")}
            checked={compare}
            onChange={(e) => setCompare(e.currentTarget.checked)}
          />
        )}
        {meta.canExport && ready && (
          <Button
            variant="light"
            leftSection={<IconDownload size={16} />}
            component="a"
            href={reportCsvUrl(kind, params)}
            data-assist="accounting.reports.csv"
          >
            {t("downloadCsv")}
          </Button>
        )}
      </Group>
      {!ready ? (
        <Text c="dimmed">{t("chooseAccount")}</Text>
      ) : report.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {refusalMessage(t, report.error)}
        </Alert>
      ) : report.isPending ? (
        <ContentSkeleton rows={8} />
      ) : (
        <ReportTable report={report.data} />
      )}
    </Stack>
  );
};

/** A report's table, any report: columns as the server names them, the rows' styles as weights. */
export const ReportTable = ({ report }: { report: AccountingReport }) => {
  const { t, amount, day, stamp } = useAccountingFormat();
  return (
    <Stack gap="xs">
      <Text fw={600}>{report.title}</Text>
      <Text size="sm" c="dimmed">
        {t("producedAt", { from: day(report.from), to: day(report.to), at: stamp(report.producedAt) })}
      </Text>
      <Table striped data-assist="accounting.reports.table">
        <Table.Thead>
          <Table.Tr>
            {report.columns.map((c) => (
              <Table.Th key={c.key} ta={c.type === "amount" ? "right" : undefined}>
                {c.label}
              </Table.Th>
            ))}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {report.rows.map((row, index) => (
            <Table.Tr key={`${index}-${row.style}`}>
              {report.columns.map((c, ci) => {
                const value = row.cells[c.key] ?? "";
                const shown = c.type === "amount" ? amount(value) : c.type === "date" ? day(value) : value;
                return (
                  <Table.Td
                    key={c.key}
                    ta={c.type === "amount" ? "right" : undefined}
                    fw={row.style === "line" ? undefined : 600}
                    pl={ci === 0 && row.depth > 0 ? row.depth * 16 : undefined}
                  >
                    {row.voucherId !== null && c.key === "voucher" ? (
                      <Anchor component={Link} to={voucherPath(row.voucherId)}>
                        {shown}
                      </Anchor>
                    ) : (
                      shown
                    )}
                  </Table.Td>
                );
              })}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  );
};
