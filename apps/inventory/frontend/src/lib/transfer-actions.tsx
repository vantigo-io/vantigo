import { Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { downloadBlob } from "../api/labels";
import { isRevisionConflict } from "../api/request";
import { cancelTransfer, deleteTransfer, type InventoryTransfer, printTransferNote } from "../api/transfers";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "./format";

/** Whether a sent transfer may still be cancelled: nothing of it received or recorded missing (I44). */
export const isCancellable = (transfer: Pick<InventoryTransfer, "status" | "lines">) =>
  transfer.status === "sent" && transfer.lines.every((line) => line.received === 0 && line.missing === 0);

/** Whether a transfer can be received: sent, or partly received (I40). */
export const isReceivable = (transfer: Pick<InventoryTransfer, "status">) =>
  transfer.status === "sent" || transfer.status === "partly_received";

/**
 * A transfer's actions that sit behind a confirmation or hand over a file:
 * delete a draft (I37), cancel a sent one (I44), print its følgeseddel (I51).
 * A refusal is said in the module's words; a stale revision reloads.
 */
export const useTransferActions = () => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();

  const failed = async (title: string, error: Error) => {
    if (isRevisionConflict(error)) {
      notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
      await invalidate();
      return;
    }
    notifications.show({ color: "red", title, message: refusalMessage(t, error) });
  };

  return {
    deleteDraft: (transfer: InventoryTransfer, after: () => void) =>
      modals.openConfirmModal({
        title: t("deleteDraftTitle", { number: transfer.number }),
        children: <Text size="sm">{t("deleteDraftBody")}</Text>,
        labels: { confirm: t("deleteDraft"), cancel: t("cancel") },
        confirmProps: { color: "red" },
        onConfirm: () =>
          void deleteTransfer(transfer.id, transfer.revision).then(
            async () => {
              notifications.show({ color: "teal", title: t("draftDeleted"), message: "" });
              await invalidate();
              after();
            },
            (error: Error) => failed(t("couldNotDelete"), error),
          ),
      }),
    /** The key is the caller's, made once and kept, so a retried cancel replays (D I14). */
    cancel: (transfer: InventoryTransfer, clientRequestId: string) =>
      modals.openConfirmModal({
        title: t("cancelTransferTitle", { number: transfer.number }),
        children: <Text size="sm">{t("cancelTransferBody", { site: transfer.fromSite.code })}</Text>,
        labels: { confirm: t("cancelTransfer"), cancel: t("keepTransfer") },
        confirmProps: { color: "red" },
        onConfirm: () =>
          void cancelTransfer(transfer.id, clientRequestId, transfer.revision).then(
            async () => {
              notifications.show({ color: "teal", title: t("transferCancelled"), message: "" });
              await invalidate();
            },
            (error: Error) => failed(t("couldNotCancel"), error),
          ),
      }),
    printNote: async (transfer: Pick<InventoryTransfer, "id" | "number">) => {
      try {
        downloadBlob(await printTransferNote(transfer.id), `folgeseddel-TRF-${transfer.number}.pdf`);
      } catch (error) {
        notifications.show({ color: "red", title: t("couldNotPrintNote"), message: refusalMessage(t, error as Error) });
      }
    },
  };
};

/** Asks before work on the screen is thrown away; resolves true when the person chooses to leave. */
export const askToLeave = (words: { title: string; body: string; leave: string; stay: string }) =>
  new Promise<boolean>((resolve) =>
    modals.openConfirmModal({
      title: words.title,
      children: <Text size="sm">{words.body}</Text>,
      labels: { confirm: words.leave, cancel: words.stay },
      confirmProps: { color: "red" },
      onConfirm: () => resolve(true),
      onCancel: () => resolve(false),
      onClose: () => resolve(false),
    }),
  );
