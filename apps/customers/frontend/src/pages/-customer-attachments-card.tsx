import { ActionIcon, Button, Card, FileButton, Group, Stack, Table, Text, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconFileTypePdf, IconPaperclip, IconPhoto, IconPlus, IconTrash } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type CustomerAttachment, customerAttachmentsQueryOptions, downloadAttachment } from "../api/attachments";
import { saveCsv } from "../api/import-export";
import {
  ATTACHMENT_ACCEPT,
  confirmDeleteAttachment,
  formatFileSize,
  useDeleteAttachment,
  useUploadAttachment,
} from "../lib/attachments";
import { formatDateOnly } from "../lib/format-date-only";
import "../i18n";

/**
 * The Attachments card on the customer page (customer attachments design
 * D7): the customer's files newest first — a contract, an NDA, the
 * correspondence behind a timeline entry — each a download, with Add
 * attachment and a Delete per row for a reader who may manage the timeline.
 * The timeline card reads the same list and shows a manual entry's own files
 * under it; both invalidate the one query key.
 */
export const CustomerAttachmentsCard = ({
  customerId,
  canManageTimeline = false,
}: {
  customerId: number;
  /** `customers:timeline-manage`, and the customer not read-only: Add and Delete are offered. */
  canManageTimeline?: boolean;
}) => {
  const { t, formatters } = useI18n("customers");
  const { data, isPending, isError } = useQuery(customerAttachmentsQueryOptions(customerId));
  const upload = useUploadAttachment(customerId);
  const remove = useDeleteAttachment(customerId);
  const attachments = data ?? [];
  const typeLabel = (type: string) => t(eventTypeKey[type] ?? "timelineEvent");

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between">
          <Group gap="xs">
            <IconPaperclip size={18} stroke={1.5} />
            <Text fw={600}>{t("attachments")}</Text>
          </Group>
          {canManageTimeline && (
            <FileButton onChange={(file) => file && upload.mutate({ file })} accept={ATTACHMENT_ACCEPT}>
              {(props) => (
                <Button
                  {...props}
                  variant="light"
                  size="xs"
                  leftSection={<IconPlus size={14} />}
                  loading={upload.isPending}
                >
                  {t("addAttachment")}
                </Button>
              )}
            </FileButton>
          )}
        </Group>
        {isPending ? (
          <ContentSkeleton rows={2} rowHeight={32} />
        ) : isError ? (
          <Text c="dimmed" size="sm">
            {t("couldNotLoadAttachments")}
          </Text>
        ) : attachments.length === 0 ? (
          <EmptyState size="sm" title={t("noAttachmentsYet")} description={t("attachmentsDescription")} />
        ) : (
          <Table.ScrollContainer minWidth={480}>
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("attachmentFile")}</Table.Th>
                  <Table.Th>{t("attachmentSize")}</Table.Th>
                  <Table.Th>{t("attachmentUploaded")}</Table.Th>
                  {canManageTimeline && <Table.Th />}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {attachments.map((attachment) => (
                  <Table.Tr key={attachment.id}>
                    <Table.Td>
                      <Stack gap={2}>
                        <AttachmentLink attachment={attachment} />
                        {attachment.timelineEntry && (
                          <Text size="xs" c="dimmed">
                            {t(
                              attachment.timelineEntry.state === "deleted"
                                ? "attachmentOnDeletedEntry"
                                : "attachmentOnEntry",
                              {
                                type: typeLabel(attachment.timelineEntry.eventType),
                                date: formatDateOnly(formatters, attachment.timelineEntry.occurredOn),
                              },
                            )}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      {formatFileSize(attachment.sizeBytes, (value) => formatters.formatNumber(value))}
                    </Table.Td>
                    <Table.Td>{formatters.formatDate(attachment.uploadedAt, { dateStyle: "medium" })}</Table.Td>
                    {canManageTimeline && (
                      <Table.Td align="right">
                        <Tooltip label={t("deleteAttachment")}>
                          <ActionIcon
                            variant="subtle"
                            color="red"
                            aria-label={t("deleteNamedAttachment", { name: attachment.fileName })}
                            loading={remove.isPending && remove.variables?.id === attachment.id}
                            onClick={() => confirmDeleteAttachment(t, attachment, () => remove.mutate(attachment))}
                          >
                            <IconTrash size={16} />
                          </ActionIcon>
                        </Tooltip>
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </Card>
  );
};

/**
 * The event types an attachment's entry can be, as the card names them —
 * the manual ones only, since only a manual entry takes an attachment.
 */
const eventTypeKey: Record<string, string> = {
  "registry.change": "registryChange",
  "interaction.call": "call",
  "interaction.meeting": "meeting",
  "interaction.email": "emailEvent",
  note: "note",
  other: "other",
};

/**
 * The file's name as the way to its bytes: fetched with the session and
 * handed to the browser (`saveCsv`), the personal-data export's shape — a
 * bare link would land the person on a problem page when the read is
 * refused. The icon says what kind of file it is.
 */
export const AttachmentLink = ({ attachment }: { attachment: CustomerAttachment }) => {
  const { t } = useI18n("customers");
  const [busy, setBusy] = useState(false);
  const Icon = attachment.contentType === "application/pdf" ? IconFileTypePdf : IconPhoto;
  const download = async () => {
    setBusy(true);
    try {
      saveCsv(await downloadAttachment(attachment));
    } catch (error) {
      notifications.show({
        color: "red",
        title: t("attachmentCouldNotBeDownloaded"),
        message: (error as Error).message,
      });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button
      variant="subtle"
      size="compact-sm"
      px={0}
      leftSection={<Icon size={16} aria-hidden="true" />}
      loading={busy}
      onClick={download}
      aria-label={t("downloadNamedAttachment", { name: attachment.fileName })}
      style={{ maxWidth: "100%" }}
    >
      <Text size="sm" truncate="end" component="span">
        {attachment.fileName}
      </Text>
    </Button>
  );
};
