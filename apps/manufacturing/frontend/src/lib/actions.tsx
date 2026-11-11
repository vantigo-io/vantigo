import { Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { isRevisionConflict } from "../api/request";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "./format";

/**
 * Archive, reactivate, make default and delete, for work centers and BoMs:
 * each sends the revision the page read, says what happened, and reloads the
 * package's queries. A stale revision says the record moved on and reloads
 * it; any other refusal is said in the module's words.
 */
export const useRecordActions = () => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();

  const run = async (work: () => Promise<unknown>, done: string, failure: string, after?: () => void) => {
    try {
      await work();
      notifications.show({ color: "teal", title: done, message: "" });
      await invalidate();
      after?.();
    } catch (error) {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        return;
      }
      notifications.show({ color: "red", title: failure, message: refusalMessage(t, error as Error) });
    }
  };

  const confirm = (title: string, body: string, label: string, onConfirm: () => void) =>
    modals.openConfirmModal({
      title,
      children: <Text size="sm">{body}</Text>,
      labels: { confirm: label, cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm,
    });

  return { run, confirm };
};
