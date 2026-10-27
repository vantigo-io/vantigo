import { Alert, Badge, Button, Pagination, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type BillingRun, billingRunsQueryOptions, runIsLive } from "../api/billing-runs";
import { invoicesMetaQueryOptions } from "../api/meta";
import { BillingRunCountBadges } from "../components/billing-run-counts";
import { RouteLink } from "../components/document-link";
import "../i18n";
import { BILLING_RUN_POLL_MS, runStatusColour, runStatusWords } from "../lib/billing-runs";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { billingRunHref, billingRunLinkOptions } from "../lib/routes";
import { NewBillingRunModal } from "./-new-billing-run-modal";

/**
 * Billing runs (energy consumption billing design D10, D11; plan reading 11):
 * the runs, newest first, a page at a time — through, issue or draft only,
 * status, the items counted by status, created — and **New billing run** for a
 * caller who may create (meta's `canCreate`). The list is read again every
 * few seconds while a run on it is still at work.
 */
export const BillingRunsPage = () => {
  const { t, date, dateTime } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const [page, setPage] = useState(1);
  const [creating, setCreating] = useState(false);
  const runs = useQuery({
    ...billingRunsQueryOptions(page),
    refetchInterval: (query) => (query.state.data?.data.some(runIsLive) ? BILLING_RUN_POLL_MS : false),
  });
  const canCreate = meta.data?.capabilities.canCreate ?? false;

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("billingRuns")}
        description={t("billingRunsDescription")}
        actions={
          canCreate && meta.data ? (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
              {t("billingRun.new")}
            </Button>
          ) : undefined
        }
      />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {refusalMessage(meta.error, t, date)}
        </Alert>
      )}
      {runs.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("billingRun.failedToLoadRuns")}>
          {refusalMessage(runs.error, t, date)}
        </Alert>
      )}
      {runs.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {runs.data && runs.data.data.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("billingRun.noRuns")}
        </Text>
      )}
      {runs.data && runs.data.data.length > 0 && (
        <Table.ScrollContainer minWidth={720}>
          <Table data-testid="billing-runs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("billingRun.col.run")}</Table.Th>
                <Table.Th>{t("billingRun.col.through")}</Table.Th>
                <Table.Th>{t("billingRun.col.mode")}</Table.Th>
                <Table.Th>{t("billingRun.col.status")}</Table.Th>
                <Table.Th>{t("billingRun.col.counts")}</Table.Th>
                <Table.Th>{t("billingRun.col.createdAt")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {runs.data.data.map((run: BillingRun) => (
                <Table.Tr key={run.id} data-testid={`billing-run-${run.id}`}>
                  <Table.Td>
                    <RouteLink href={billingRunHref(run.id)} to={billingRunLinkOptions(run.id)}>
                      {t("billingRun.name", { id: run.id })}
                    </RouteLink>
                  </Table.Td>
                  <Table.Td>{date(run.through)}</Table.Td>
                  <Table.Td>{run.issue ? t("billingRun.mode.issue") : t("billingRun.mode.draft")}</Table.Td>
                  <Table.Td>
                    <Badge variant="light" color={runStatusColour[run.status] ?? "gray"}>
                      {runStatusWords(run.status, t)}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <BillingRunCountBadges counts={run.counts} />
                  </Table.Td>
                  <Table.Td>{dateTime(run.createdAt)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      {runs.data && runs.data.pagination.totalPages > 1 && (
        <Pagination total={runs.data.pagination.totalPages} value={page} onChange={setPage} />
      )}
      {creating && meta.data && (
        <NewBillingRunModal
          today={meta.data.today}
          canIssue={meta.data.capabilities.canIssue}
          onClose={() => setCreating(false)}
        />
      )}
    </Stack>
  );
};
