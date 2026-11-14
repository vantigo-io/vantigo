import { Alert, Anchor, Badge, Button, Card, Group, List, Select, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconDownload, IconFileExport, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingMeta,
  type AccountingSaftExport,
  createSaftExport,
  saftExportCheckQueryOptions,
  saftExportFileUrl,
  saftExportsQueryOptions,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { ACCOUNTS_ROUTE_PATH } from "../lib/routes";

/** The fiscal year a day falls in: the calendar year in which it begins. */
const fiscalYearOf = (date: string, startMonth: number) => {
  const year = Number(date.slice(0, 4));
  return Number(date.slice(5, 7)) < startMonth ? year - 1 : year;
};

/**
 * SAF-T Financial (A24): the file an audit asks for. The person picks a
 * fiscal year or periods within it, sees first which accounts export NA for
 * want of a grouping code, and asks for the file; the worker writes it and
 * the list offers the download, with who asked and when.
 */
export const SaftExportPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navSaft")} description={t("saftDescription")} />
          {meta.canExport ? (
            <>
              <ExportForm meta={meta} />
              <Exports />
            </>
          ) : (
            <Alert color="blue" icon={<IconInfoCircle size={16} />}>
              {t("saftNeedsExport")}
            </Alert>
          )}
        </Stack>
      )}
    </BooksGate>
  );
};

const ExportForm = ({ meta }: { meta: AccountingMeta }) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const settings = meta.settings;
  const startMonth = settings?.fiscalYearStartMonth ?? 1;
  const first = fiscalYearOf(settings?.booksStart ?? meta.today, startMonth);
  const last = fiscalYearOf(meta.today, startMonth);
  const years = Array.from({ length: Math.max(1, last - first + 1) }, (_, i) => String(last - i));
  const [year, setYear] = useState<string | null>(years[0] ?? null);
  const [from, setFrom] = useState<string | null>("1");
  const [to, setTo] = useState<string | null>("12");
  const periods = Array.from({ length: 12 }, (_, i) => ({
    value: String(i + 1),
    label: t("saftPeriod", { number: i + 1, month: t(`month_${((startMonth - 1 + i) % 12) + 1}`) }),
  }));
  const fiscalYear = Number(year);
  const fromPeriod = Number(from);
  const toPeriod = Number(to);
  const check = useQuery({
    ...saftExportCheckQueryOptions(fiscalYear, fromPeriod, toPeriod),
    enabled: Boolean(year) && fromPeriod <= toPeriod,
  });
  const create = useMutation({
    mutationFn: () => createSaftExport({ fiscalYear, fromPeriod, toPeriod }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY, "saft", "exports"] });
      notifications.show({ color: "green", message: t("saftQueued") });
    },
  });
  const unmapped = check.data?.unmappedAccounts ?? [];
  return (
    <Card withBorder padding="lg">
      <Stack gap="md">
        <Group align="flex-end">
          <Select
            label={t("saftFiscalYear")}
            data={years}
            value={year}
            onChange={setYear}
            allowDeselect={false}
            w={140}
            data-assist="accounting.saft.year"
          />
          <Select label={t("saftFromPeriod")} data={periods} value={from} onChange={setFrom} allowDeselect={false} />
          <Select label={t("saftToPeriod")} data={periods} value={to} onChange={setTo} allowDeselect={false} />
        </Group>
        {fromPeriod > toPeriod && <Text c="red">{t("saftPeriodsOutOfOrder")}</Text>}
        {check.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
            {refusalMessage(t, check.error)}
          </Alert>
        )}
        {check.data?.sellerMissing && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("saftSellerMissing")}>
            {t("saftSellerMissingHint")}
          </Alert>
        )}
        {unmapped.length > 0 && (
          <Alert
            color="yellow"
            icon={<IconAlertTriangle size={16} />}
            title={t("saftUnmapped", { count: unmapped.length })}
          >
            <Stack gap="xs">
              <Text size="sm">{t("saftUnmappedHint")}</Text>
              <List size="sm">
                {unmapped.slice(0, 20).map((a) => (
                  <List.Item key={a.accountId}>
                    {a.number} {a.name}
                  </List.Item>
                ))}
              </List>
              <Anchor component={Link} to={ACCOUNTS_ROUTE_PATH} size="sm">
                {t("saftFixInChart")}
              </Anchor>
            </Stack>
          </Alert>
        )}
        {check.data && (
          <Text size="sm" c="dimmed">
            {t("saftVouchers", { count: check.data.vouchers })}
          </Text>
        )}
        <Text size="sm" c="dimmed">
          {t("saftSensitive")}
        </Text>
        {create.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, create.error)}
          </Alert>
        )}
        <Group>
          <Button
            leftSection={<IconFileExport size={16} />}
            loading={create.isPending}
            disabled={!year || fromPeriod > toPeriod || check.data?.sellerMissing === true}
            onClick={() => create.mutate()}
            data-assist="accounting.saft.export"
          >
            {t("saftExport")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};

const StateBadge = ({ state }: { state: AccountingSaftExport["state"] }) => {
  const { t } = useAccountingFormat();
  const color = { queued: "gray", running: "blue", done: "green", failed: "red" }[state];
  return <Badge color={color}>{t(`saftState_${state}`)}</Badge>;
};

/** Every export asked for, newest first, with its files. */
const Exports = () => {
  const { t, stamp, day } = useAccountingFormat();
  const exports = useQuery(saftExportsQueryOptions());
  if (exports.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(t, exports.error)}
      </Alert>
    );
  if (exports.isPending) return <ContentSkeleton rows={4} />;
  if (exports.data.length === 0) return <Text c="dimmed">{t("saftNoExports")}</Text>;
  return (
    <Table striped data-assist="accounting.saft.exports">
      <Table.Thead>
        <Table.Tr>
          <Table.Th>{t("saftAskedAt")}</Table.Th>
          <Table.Th>{t("saftAskedBy")}</Table.Th>
          <Table.Th>{t("saftSelection")}</Table.Th>
          <Table.Th>{t("saftState")}</Table.Th>
          <Table.Th>{t("saftFiles")}</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {exports.data.map((e) => (
          <Table.Tr key={e.id}>
            <Table.Td>{stamp(e.requestedAt)}</Table.Td>
            <Table.Td>{e.requestedByName ?? ""}</Table.Td>
            <Table.Td>
              {day(e.from)} – {day(e.to)}
            </Table.Td>
            <Table.Td>
              <Stack gap={4}>
                <StateBadge state={e.state} />
                {e.error && (
                  <Text size="xs" c="red">
                    {e.error}
                  </Text>
                )}
              </Stack>
            </Table.Td>
            <Table.Td>
              <Stack gap={4}>
                {e.files.map((f) => (
                  <Anchor key={f.part} href={saftExportFileUrl(e.id, f.part)} size="sm">
                    <Group gap={4} wrap="nowrap">
                      <IconDownload size={14} />
                      {f.fileName} ({t("saftSize", { size: (f.sizeBytes / 1024 / 1024).toFixed(1) })})
                    </Group>
                  </Anchor>
                ))}
              </Stack>
            </Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  );
};
