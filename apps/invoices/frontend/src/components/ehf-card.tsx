import { Button, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDownload, IconSend } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { cancelTransmission, downloadUbl, type Transmission } from "../api/ehf";
import { isSessionExpired, saveCsv } from "../api/export";
import type { InvoiceDocument } from "../api/invoices";
import { INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { ResolveTransmissionModal } from "../pages/-resolve-transmission-modal";
import { EhfBadge } from "./ehf-badge";

export interface EhfCardProps {
  /** An issued document with its `ehf` block. */
  document: InvoiceDocument;
  /** meta's `canIssue`: cancel, resolve and the reason a send is blocked are an issuer's. */
  canIssue: boolean;
  /** Whether the card offers Send as EHF — false when the page's primary action already does (D10). */
  offerSend: boolean;
  onSend: () => void;
}

/**
 * The E-invoice card (EHF and KID design D10, D15): the state in honest
 * words, the latest transmission's timestamps, the provider's reference and
 * the reason for an issuer, every transmission newest first — Cancel on a
 * queued one (the server refuses it once a submission may have reached the
 * provider), Resolve on an unconfirmed one, and its UBL to download — and
 * Send as EHF when the document can be sent, or why it cannot.
 */
export const EhfCard = ({ document: doc, canIssue, offerSend, onSend }: EhfCardProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [resolving, setResolving] = useState<Transmission | null>(null);
  const ehf = doc.ehf;
  const cancel = useMutation({
    mutationFn: (transmissionId: number) => cancelTransmission(doc.id, transmissionId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("transmissionCancelled") });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("couldNotCancelTransmission"),
        message: refusalMessage(error, t, date),
      }),
  });
  const download = useMutation({
    mutationFn: (row: Transmission) => downloadUbl(doc.id, row.id, `ehf-${doc.number ?? doc.id}-${row.id}.xml`),
    onSuccess: saveCsv,
    onError: (error) => {
      if (isSessionExpired(error)) return;
      notifications.show({ color: "red", title: t("couldNotDownloadUbl"), message: refusalMessage(error, t, date) });
    },
  });
  if (!ehf) return null;
  const timestamps = [
    ["ehfQueuedAt", ehf.queuedAt],
    ["ehfSubmittedAt", ehf.submittedAt],
    ["ehfDeliveredAt", ehf.deliveredAt],
    ["ehfFailedAt", ehf.failedAt],
  ] as const;
  // Why it cannot be sent, for an issuer: the state already says an
  // ehf_already_sent, and an installation that cannot send EHF points to the
  // settings rather than to a key the reader may not manage.
  const blocked =
    !ehf.canSend && canIssue && ehf.blockedBy && ehf.blockedBy !== "ehf_already_sent"
      ? ehf.blockedBy === "ehf_unavailable"
        ? t("ehfUnavailableHint")
        : refusalMessage(Object.assign(new Error(ehf.blockedBy), { code: ehf.blockedBy }), t, date)
      : undefined;
  return (
    <Card withBorder data-testid="ehf-card">
      <Stack gap="xs">
        <Group justify="space-between">
          <Group gap="xs">
            <Title order={4}>{t("eInvoice")}</Title>
            <EhfBadge status={ehf.status} long testId="ehf-status" />
          </Group>
          {offerSend && ehf.canSend && (
            <Button size="xs" variant="default" leftSection={<IconSend size={14} />} onClick={onSend}>
              {t("sendAsEhf")}
            </Button>
          )}
        </Group>
        {timestamps.map(([key, at]) =>
          at ? (
            <Text key={key} size="sm">
              {t(key, { at: dateTime(at) })}
            </Text>
          ) : null,
        )}
        {ehf.providerRef && <Text size="sm">{t("ehfProviderRef", { ref: ehf.providerRef })}</Text>}
        {ehf.reason && <Text size="sm">{t("ehfReason", { reason: ehf.reason })}</Text>}
        {blocked && (
          <Text size="sm" c="dimmed" data-testid="ehf-blocked">
            {blocked}
          </Text>
        )}
        {ehf.transmissions.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noTransmissions")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={560}>
            <Table aria-label={t("transmissions")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("ehfColumnQueued")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("receiver")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {ehf.transmissions.map((row) => (
                  <Table.Tr key={row.id} data-testid="transmission" data-transmission={row.id}>
                    <Table.Td>{dateTime(row.queuedAt)}</Table.Td>
                    <Table.Td>
                      <Stack gap={2}>
                        <EhfBadge status={row.status} />
                        {row.cancelledAt && (
                          <Text size="xs" c="dimmed">
                            {t("ehfCancelledAt", { at: dateTime(row.cancelledAt) })}
                          </Text>
                        )}
                        {row.resolutionNote && (
                          <Text size="xs" c="dimmed">
                            {t("ehfResolvedNote", { note: row.resolutionNote })}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>{row.receiverParticipant}</Table.Td>
                    <Table.Td>
                      <Group gap={4} justify="flex-end" wrap="nowrap">
                        {canIssue && row.status === "queued" && (
                          <Button
                            size="xs"
                            variant="subtle"
                            color="red"
                            loading={cancel.isPending}
                            onClick={() => cancel.mutate(row.id)}
                          >
                            {t("cancelTransmission")}
                          </Button>
                        )}
                        {canIssue && row.status === "unconfirmed" && (
                          <Button size="xs" variant="light" onClick={() => setResolving(row)}>
                            {t("resolveTransmission")}
                          </Button>
                        )}
                        <Button
                          size="xs"
                          variant="subtle"
                          leftSection={<IconDownload size={14} />}
                          loading={download.isPending && download.variables?.id === row.id}
                          onClick={() => download.mutate(row)}
                        >
                          {t("downloadUbl")}
                        </Button>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
      {resolving && (
        <ResolveTransmissionModal document={doc} transmission={resolving} onClose={() => setResolving(null)} />
      )}
    </Card>
  );
};
