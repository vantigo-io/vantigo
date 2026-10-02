import { Alert, Button, Group, Pagination, Stack, Table, Text, Title } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconCircleCheck, IconDownload } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { downloadInvoicesCsv, isSessionExpired, saveCsv } from "../api/export";
import { journalQueryOptions } from "../api/journal";
import { invoicesMetaQueryOptions } from "../api/meta";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

const firstOfMonth = (day: string) => `${day.slice(0, 7)}-01`;

/**
 * The invoice journal (D11, D12): a range of issue dates, the gap check in
 * words, the totals per SAF-T code over the whole range, and the documents in
 * number order a page at a time — credit notes signed negative. "Export CSV"
 * fetches the accountant's file for the range shown (payments and delivery
 * design D5), so a refusal is a notification, never a problem opened in the
 * browser.
 */
export const JournalPage = () => {
  const { t, money, date, number } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const today = meta.data?.today ?? "";
  const [from, setFrom] = useState<string | null>(null);
  const [to, setTo] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const rangeFrom = from ?? (today ? firstOfMonth(today) : "");
  const rangeTo = to ?? today;
  const journal = useQuery({
    ...journalQueryOptions(rangeFrom, rangeTo, page),
    enabled: Boolean(rangeFrom && rangeTo),
  });
  const data = journal.data;
  // The server's own facts, never the page's: the numbers the gap check
  // covered (`checkedFrom`–`checkedTo`, which can start before the range's
  // first document), and the highest number issued in the whole series, which
  // the counter must equal (D11). Neither a gap nor a counter ahead of the
  // documents happens in normal use — the counter rolls back with a refused
  // issue and an issued row cannot be deleted — so both are said as alarms.
  const checked = data?.checkedFrom !== undefined && data?.checkedTo !== undefined;
  const counterAhead = data?.counterLast !== undefined && data.counterLast !== data.highestIssued;
  const exportCsv = useMutation({
    mutationFn: () => downloadInvoicesCsv({ from: rangeFrom, to: rangeTo }),
    onSuccess: saveCsv,
    onError: (error) => {
      // An expired session has signed the person out already: nothing to say here.
      if (isSessionExpired(error)) return;
      notifications.show({ color: "red", title: t("couldNotExport"), message: refusalMessage(error, t, date) });
    },
  });

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("journal")}
        description={t("journalDescription")}
        actions={
          <Button
            variant="default"
            leftSection={<IconDownload size={16} />}
            disabled={!rangeFrom || !rangeTo}
            loading={exportCsv.isPending}
            onClick={() => exportCsv.mutate()}
          >
            {t("exportCsv")}
          </Button>
        }
      />
      <Group>
        <DateInput
          label={t("issuedFrom")}
          valueFormat={t("dateInputFormat")}
          value={rangeFrom || null}
          onChange={(d) => {
            setFrom(d);
            setPage(1);
          }}
        />
        <DateInput
          label={t("issuedTo")}
          valueFormat={t("dateInputFormat")}
          value={rangeTo || null}
          onChange={(d) => {
            setTo(d);
            setPage(1);
          }}
        />
      </Group>
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {refusalMessage(meta.error, t, date)}
        </Alert>
      )}
      {journal.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadJournal")}>
          {refusalMessage(journal.error, t, date)}
        </Alert>
      )}
      {/* The range starts from meta's today, so without meta the journal is never asked for. */}
      {!meta.isError && journal.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {data && meta.data && (
        <>
          {!checked ? (
            <Alert color="gray" icon={<IconCircleCheck size={16} />}>
              {t("noDocumentsInRange")}
            </Alert>
          ) : data.gaps.length === 0 ? (
            <Alert color="green" icon={<IconCircleCheck size={16} />}>
              {t("noGaps", { first: data.checkedFrom, last: data.checkedTo })}
            </Alert>
          ) : (
            <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("gapsFound")}>
              <Stack gap={4}>
                <Text size="sm">{t("missingNumbers", { numbers: data.gaps.join(", ") })}</Text>
                {data.gapsTruncated && <Text size="sm">{t("gapsTruncated")}</Text>}
                <Text size="sm">{t("checkedRange", { first: data.checkedFrom, last: data.checkedTo })}</Text>
                <Text size="sm">{t("seriesBrokenHint")}</Text>
              </Stack>
            </Alert>
          )}
          {counterAhead && (
            <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("counterAheadTitle")}>
              <Stack gap={4}>
                <Text size="sm">
                  {t("counterAhead", { counter: data.counterLast, highest: data.highestIssued ?? t("notAvailable") })}
                </Text>
                <Text size="sm">{t("seriesBrokenHint")}</Text>
              </Stack>
            </Alert>
          )}
          {/* The totals are in the installation's currency, meta's — each row keeps its own. */}
          <Title order={4}>{t("totalsByCode")}</Title>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("safTCode")}</Table.Th>
                <Table.Th>{t("category")}</Table.Th>
                <Table.Th ta="right">{t("ratePercent")}</Table.Th>
                <Table.Th ta="right">{t("taxableAmount")}</Table.Th>
                <Table.Th ta="right">{t("vatTotal")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {data.totals.byCode.map((c) => (
                <Table.Tr key={`${c.safTCode}-${c.category}-${c.ratePercent}`}>
                  <Table.Td>{c.safTCode}</Table.Td>
                  <Table.Td>{c.category}</Table.Td>
                  <Table.Td ta="right">{number(c.ratePercent, 2)}</Table.Td>
                  <Table.Td ta="right">{money(c.taxableAmount, meta.data.currency)}</Table.Td>
                  <Table.Td ta="right">{money(c.vatAmount, meta.data.currency)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          <Text fw={600}>
            {t("journalTotals", {
              net: money(data.totals.netTotal, meta.data.currency),
              vat: money(data.totals.vatTotal, meta.data.currency),
              gross: money(data.totals.grossTotal, meta.data.currency),
            })}
          </Text>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("number")}</Table.Th>
                <Table.Th>{t("kind")}</Table.Th>
                <Table.Th>{t("issueDate")}</Table.Th>
                <Table.Th>{t("customer")}</Table.Th>
                <Table.Th ta="right">{t("netTotal")}</Table.Th>
                <Table.Th ta="right">{t("vatTotal")}</Table.Th>
                <Table.Th ta="right">{t("grossTotal")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {data.data.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Td>{row.number}</Table.Td>
                  <Table.Td>
                    {row.kind === "credit_note" ? t("creditNoteFor", { number: row.creditsNumber }) : t("kindInvoice")}
                  </Table.Td>
                  <Table.Td>{date(row.issueDate)}</Table.Td>
                  <Table.Td>{row.buyerName}</Table.Td>
                  <Table.Td ta="right">{money(row.netTotal, row.currency)}</Table.Td>
                  <Table.Td ta="right">{money(row.vatTotal, row.currency)}</Table.Td>
                  <Table.Td ta="right">{money(row.grossTotal, row.currency)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          {data.pagination.totalPages > 1 && (
            <Pagination total={data.pagination.totalPages} value={page} onChange={setPage} />
          )}
        </>
      )}
    </Stack>
  );
};
