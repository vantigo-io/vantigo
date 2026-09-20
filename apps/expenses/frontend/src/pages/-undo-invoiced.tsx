import { Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { undoExpenseInvoiced } from "../api/approvals";
import type { Expense } from "../api/entries";
import { type ApiError, EXPENSES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useLineName } from "../lib/line-name";

/**
 * Taking the invoicing back off one line — the other end of
 * `POST /entries/{id}/invoiced`, under exactly the rights that set it and with
 * the same revision guard.
 *
 * It is asked out loud rather than done on a click: what the stamp records is
 * that the line went out on an invoice somebody sent, and taking that back is
 * a claim about the world, not a preference. Both drawers that can invoice a
 * line can undo one, and both do it through here so the sentence and the
 * revision chain are written once.
 *
 * The revision is the caller's own — whatever its last write answered — so an
 * undo straight after a pricing does not race itself into a 409.
 */
export const useUndoInvoiced = (onSaved: (line: Expense) => void) => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  const lineName = useLineName();

  const undo = useMutation({
    mutationFn: (line: Expense) => undoExpenseInvoiced(line.id, { revision: line.revision }),
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("invoicingUndone"), message: lineName(saved) });
      onSaved(saved);
    },
    onError: (error) => {
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotUndoInvoiced"),
        message: conflict ? t("expenseChangedElsewhere") : refusalMessage(error),
      });
    },
  });

  return {
    isPending: undo.isPending,
    confirm: (line: Expense) =>
      modals.openConfirmModal({
        title: t("undoInvoicedTitle"),
        children: <Text size="sm">{t("undoInvoicedConfirm", { description: lineName(line) })}</Text>,
        labels: { confirm: t("undoInvoiced"), cancel: t("cancel") },
        confirmProps: { color: "red" },
        onConfirm: () => undo.mutate(line),
      }),
  };
};
