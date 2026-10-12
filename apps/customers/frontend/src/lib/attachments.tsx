import { Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { attachmentsQueryKey, type CustomerAttachment, deleteAttachment, uploadAttachment } from "../api/attachments";
import { ApiValidationError } from "../api/request";
import { customerWriteErrorMessage } from "./customer-write-error";
import "../i18n";

/**
 * What the file picker offers (customer attachments design D2): the four
 * types the server takes, by extension and by type, so a phone's camera roll
 * and a desktop's file dialog both narrow to them. The server decides by the
 * bytes; this only keeps an obviously wrong file from a round trip.
 */
export const ATTACHMENT_ACCEPT =
  ".pdf,.jpg,.jpeg,.jpe,.png,.heic,.heif,application/pdf,image/jpeg,image/png,image/heic,image/heif";

/** The server's own bound (`maxAttachmentBytes`): a larger file is refused here rather than sent to be refused. */
export const MAX_ATTACHMENT_BYTES = 10 * 1024 * 1024;

/** A file size written for a human: bytes under a kilobyte, then kB, then MB (Expenses' receipts). */
export const formatFileSize = (bytes: number, formatNumber: (value: number) => string): string => {
  if (bytes < 1024) return `${formatNumber(bytes)} B`;
  if (bytes < 1024 * 1024) return `${formatNumber(Math.round(bytes / 1024))} kB`;
  return `${formatNumber(Math.round((bytes / (1024 * 1024)) * 10) / 10)} MB`;
};

/** The delete's confirmation, shared with the timeline's per-entry delete. */
export const confirmDeleteAttachment = (
  t: (key: string, options?: Record<string, unknown>) => string,
  attachment: CustomerAttachment,
  onConfirm: () => void,
) =>
  modals.openConfirmModal({
    title: t("deleteAttachment"),
    children: <Text size="sm">{t("deleteAttachmentQuestion", { name: attachment.fileName })}</Text>,
    labels: { confirm: t("delete"), cancel: t("cancel") },
    confirmProps: { color: "red" },
    onConfirm,
  });

/** What a refused upload says: the server's field message on `file` or `timelineEntryId`, else its sentence. */
const uploadRefusal = (error: Error, t: (key: string) => string): string => {
  if (error instanceof ApiValidationError) {
    const named = error.fields.file ?? error.fields.timelineEntryId ?? Object.values(error.fields).flat();
    if (named.length > 0) return named.join(" ");
  }
  return customerWriteErrorMessage(error, t);
};

/**
 * The upload, to the customer or to one of its manual entries: a file over
 * the server's bound is refused here rather than sent to be refused; the
 * list is refreshed on success, and the timeline too, since every upload is
 * an event on it.
 */
export const useUploadAttachment = (customerId: number) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ file, timelineEntryId }: { file: File; timelineEntryId?: number | null }) => {
      if (file.size > MAX_ATTACHMENT_BYTES) return Promise.reject(new Error(t("attachmentTooLarge")));
      return uploadAttachment(customerId, file, timelineEntryId);
    },
    onSuccess: (attachment) => {
      notifications.show({
        color: "teal",
        title: t("attachmentAdded"),
        message: t("attachmentAddedMessage", { name: attachment.fileName }),
      });
      queryClient.invalidateQueries({ queryKey: attachmentsQueryKey(customerId) });
      queryClient.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] });
    },
    onError: (error: Error) => {
      notifications.show({ color: "red", title: t("attachmentCouldNotBeAdded"), message: uploadRefusal(error, t) });
    },
  });
};

/** The delete: the row and, once it has committed, the file; the list and the timeline refreshed. */
export const useDeleteAttachment = (customerId: number) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (attachment: CustomerAttachment) => deleteAttachment(customerId, attachment.id),
    onSuccess: (_, attachment) => {
      notifications.show({
        color: "teal",
        title: t("attachmentRemoved"),
        message: t("attachmentRemovedMessage", { name: attachment.fileName }),
      });
      queryClient.invalidateQueries({ queryKey: attachmentsQueryKey(customerId) });
      queryClient.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] });
    },
    onError: (error: Error) => {
      queryClient.invalidateQueries({ queryKey: attachmentsQueryKey(customerId) });
      notifications.show({
        color: "red",
        title: t("attachmentCouldNotBeRemoved"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });
};
