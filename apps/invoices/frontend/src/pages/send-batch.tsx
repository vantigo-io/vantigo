import { Alert, Badge, Button, Group, Modal, Progress, Stack, Table, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArrowLeft } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import { INVOICES_QUERY_KEY } from "../api/request";
import {
  batchIsLive,
  countKey,
  postSendBatchPosted,
  SEND_JOB_STATES,
  type SendBatchCounts,
  type SendJob,
  sendBatchPrintUrl,
  sendBatchQueryOptions,
  useSendBatchesLive,
} from "../api/send-batches";
import { DocumentLink, RouteLink } from "../components/document-link";
import { PdfButton } from "../components/pdf-button";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { billingRunHref, billingRunLinkOptions } from "../lib/routes";
import { jobChannelWords, jobCodeWords, jobStateColour, jobStateWords } from "../lib/send-batches";

export interface SendBatchPageProps {
  batchId: number;
}

/** The states that end a job: everything but queued and sending. */
const settled = (counts: SendBatchCounts) =>
  SEND_JOB_STATES.filter((s) => s !== "queued" && s !== "sending").reduce((n, s) => n + counts[countKey[s]], 0);

/**
 * One Send all (invoices phase 6 design I36, plan E6): its documents counted
 * by state and a progress bar while the worker carries them, then each
 * document's number, customer, channel, outcome and — when it was skipped,
 * failed or is uncertain — why, in words. Documents left for printing are
 * downloaded as one ZIP of their stored PDFs and marked as posted with the
 * day. The page updates by itself; with the live channel unavailable it reads
 * the batch again every few seconds until it is finished.
 */
export const SendBatchPage = ({ batchId }: SendBatchPageProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const live = useSendBatchesLive(batchId);
  const detail = useQuery(sendBatchQueryOptions(batchId, live));
  const [posting, setPosting] = useState(false);
  const batch = detail.data?.batch;
  const canIssue = meta.data?.capabilities.canIssue ?? false;
  const total = detail.data?.jobs.length ?? 0;
  const awaitingPrint = batch?.counts.awaitingPrint ?? 0;

  return (
    <Stack gap="lg">
      {batch?.billingRunId !== undefined && (
        <RouteLink href={billingRunHref(batch.billingRunId)} to={billingRunLinkOptions(batch.billingRunId)}>
          <Group gap={4} component="span">
            <IconArrowLeft size={14} />
            {t("sendAll.backToRun", { id: batch.billingRunId })}
          </Group>
        </RouteLink>
      )}
      <PageHeader
        title={t("sendAll.batchName", { id: batchId })}
        actions={
          canIssue && awaitingPrint > 0 ? (
            <Group gap="xs">
              <PdfButton
                data-assist="invoices.send-batch.print"
                variant="default"
                url={sendBatchPrintUrl(batchId)}
                mode="download"
              >
                {t("sendAll.downloadForPrinting")}
              </PdfButton>
              <Button onClick={() => setPosting(true)}>{t("sendAll.markPosted")}</Button>
            </Group>
          ) : undefined
        }
      />
      {detail.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {detail.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("sendAll.failedToLoad")}>
          {refusalMessage(detail.error, t, date)}
        </Alert>
      )}
      {batch && detail.data && (
        <>
          <PageSection>
            <Stack gap="xs">
              <Text size="sm">{t("sendAll.createdLine", { at: dateTime(batch.createdAt) })}</Text>
              {batch.finishedAt ? (
                <Text size="sm">{t("sendAll.finishedLine", { at: dateTime(batch.finishedAt) })}</Text>
              ) : (
                <Progress
                  value={total === 0 ? 0 : (settled(batch.counts) / total) * 100}
                  aria-label={t("sendAll.progress")}
                  data-testid="send-batch-progress"
                />
              )}
              <Group gap={4} data-testid="send-batch-counts">
                {SEND_JOB_STATES.filter((s) => batch.counts[countKey[s]] > 0).map((s) => (
                  <Badge key={s} variant="light" color={jobStateColour[s]}>
                    {t("billingRun.count", { status: jobStateWords(s, t), count: batch.counts[countKey[s]] })}
                  </Badge>
                ))}
              </Group>
              {batch.counts.uncertain > 0 && (
                <Alert color="orange" icon={<IconAlertCircle size={16} />} title={t("sendAll.uncertainTitle")}>
                  {t("sendAll.uncertainBody")}
                </Alert>
              )}
            </Stack>
          </PageSection>
          <PageSection>
            <Table.ScrollContainer minWidth={720}>
              <Table data-testid="send-batch-jobs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("sendAll.col.document")}</Table.Th>
                    <Table.Th>{t("sendAll.col.customer")}</Table.Th>
                    <Table.Th>{t("sendAll.col.channel")}</Table.Th>
                    <Table.Th>{t("sendAll.col.outcome")}</Table.Th>
                    <Table.Th>{t("sendAll.col.reason")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {detail.data.jobs.map((job) => (
                    <JobRow key={job.id} job={job} />
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </PageSection>
        </>
      )}
      {posting && batch && (
        <PostedModal batchId={batchId} earliest={batch.createdAt} onClose={() => setPosting(false)} />
      )}
      {batch && batchIsLive(batch) && !live && (
        <Text size="xs" c="dimmed">
          {t("sendAll.polling")}
        </Text>
      )}
    </Stack>
  );
};

const JobRow = ({ job }: { job: SendJob }) => {
  const { t } = useInvoiceFormat();
  return (
    <Table.Tr data-testid={`send-job-${job.id}`}>
      <Table.Td>
        <DocumentLink invoiceId={job.invoiceId}>
          {job.number !== undefined
            ? t("billingRun.documentNumber", { number: job.number })
            : t("billingRun.documentDraft")}
        </DocumentLink>
      </Table.Td>
      <Table.Td>
        {job.customerName ?? (job.customerId !== undefined ? t("billingRun.customerById", { id: job.customerId }) : "")}
      </Table.Td>
      <Table.Td>{job.channel ? jobChannelWords(job.channel, t) : t("notAvailable")}</Table.Td>
      <Table.Td>
        <Badge variant="light" color={jobStateColour[job.state] ?? "gray"}>
          {jobStateWords(job.state, t)}
        </Badge>
      </Table.Td>
      <Table.Td>
        {job.code && (
          <Stack gap={2}>
            <Text size="sm">{jobCodeWords(job.code, t)}</Text>
            {job.detail && (
              <Text size="xs" c="dimmed">
                {job.detail}
              </Text>
            )}
          </Stack>
        )}
      </Table.Td>
    </Table.Tr>
  );
};

/** **Mark as posted**: the day the printed documents went in the post, from the batch's day to today. */
const PostedModal = ({ batchId, earliest, onClose }: { batchId: number; earliest: string; onClose: () => void }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const meta = useQuery(invoicesMetaQueryOptions());
  const today = meta.data?.today;
  const [postedOn, setPostedOn] = useState<string | null>(today ?? null);
  const posted = useMutation({
    mutationFn: () => postSendBatchPosted(batchId, postedOn as string),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("sendAll.postedDone") });
      onClose();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("sendAll.couldNotPost"), message: refusalMessage(error, t, date) }),
  });
  return (
    <Modal opened onClose={onClose} title={t("sendAll.markPosted")}>
      <Stack>
        <Text size="sm">{t("sendAll.postedBody")}</Text>
        <DateInput
          label={t("sendAll.postedOn")}
          valueFormat={t("dateInputFormat")}
          value={postedOn}
          minDate={earliest.slice(0, 10)}
          maxDate={today}
          onChange={setPostedOn}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={!postedOn} loading={posted.isPending} onClick={() => posted.mutate()}>
            {t("sendAll.markPosted")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
