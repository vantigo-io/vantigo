import { Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { undoExpenseInvoiced } from "../api/approvals";
import type { Expense } from "../api/entries";
import { type ApiError, EXPENSES_QUERY_KEY } from "../api/request";
import "../i18n";
import { invoicedByInvoicesNumber, refusalMessage } from "../lib/errors";
import { useLineName } from "../lib/line-name";

/**
 * Whether the undo is offered: the line's own capability, and never on a line
 * the Invoices module invoiced (invoices work design D1, D18) — that mark is
 * the invoice's, and only a credit note returning the line takes it back, so
 * the server would refuse it with `invoiced_by_invoices`.
 */
export const canUndoInvoiced = (line: Expense): boolean =>
  line.capabilities.canUndoInvoiced && line.billing?.invoice?.invoicedBy === undefined;

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
      // A line the Invoices module invoiced since it was read: its mark is
      // that invoice's, said in words rather than as a stale form.
      const invoiced = invoicedByInvoicesNumber(error);
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotUndoInvoiced"),
        message:
          invoiced !== undefined
            ? t("invoicedByInvoicesRefusal", { number: invoiced })
            : conflict
              ? t("expenseChangedElsewhere")
              : refusalMessage(error),
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
