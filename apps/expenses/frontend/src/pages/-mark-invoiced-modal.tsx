import { Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { markExpenseInvoiced } from "../api/approvals";
import type { Expense } from "../api/entries";
import { type ApiError, ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";

/** What the contract allows in an invoice reference, once trimmed. */
export const INVOICE_REFERENCE_MAX_LENGTH = 100;

export interface MarkInvoicedModalProps {
  /** The approved billable line being billed on, or null when the modal is closed. */
  expense: Expense | null;
  /** The revision the drawer was opened at, or the one its last write answered. */
  revision: number | undefined;
  onClose: () => void;
  onSaved: (expense: Expense) => void;
  /**
   * A 409: the line moved on under the caller. A drawer that lives off a query
   * reads it again here, so reopening this dialog does not send the same
   * revision and earn the same refusal.
   */
  onConflict?: () => void;
}

/**
 * Marking one line as billed on to the customer (decision X5). It is the
 * project's own door, not `expenses:manage`'s — `capabilities.canMarkInvoiced`
 * is the gate — and the period lock does not reach it, because an invoice for
 * December goes out in January.
 *
 * The reference is optional and is the only thing asked for: what the customer
 * was billed was decided at the pricing door, and this records that it went
 * out.
 */
export const MarkInvoicedModal = ({ expense, revision, onClose, onConflict, onSaved }: MarkInvoicedModalProps) => {
  const { t } = useI18n("expenses");
  return (
    <Modal opened={expense !== null} onClose={onClose} title={t("markInvoicedTitle")} centered>
      {expense && (
        <InvoiceForm
          key={expense.id}
          expense={expense}
          revision={revision}
          onClose={onClose}
          onConflict={onConflict}
          onSaved={onSaved}
        />
      )}
    </Modal>
  );
};

const InvoiceForm = ({
  expense,
  revision,
  onClose,
  onConflict,
  onSaved,
}: MarkInvoicedModalProps & { expense: Expense }) => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  const form = useForm({
    initialValues: { reference: "" },
    validate: {
      reference: (value) => (value.trim().length > INVOICE_REFERENCE_MAX_LENGTH ? t("referenceTooLong") : null),
    },
  });

  const save = useMutation({
    mutationFn: (values: { reference: string }) =>
      markExpenseInvoiced(expense.id, {
        revision: revision ?? expense.revision,
        ...(values.reference.trim() ? { reference: values.reference.trim() } : {}),
      }),
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("markedInvoiced"), message: expense.description });
      onSaved(saved);
    },
    onError: (error) => {
      if (error instanceof ApiValidationError && error.fieldErrors.reference) {
        form.setErrors({ reference: error.fieldErrors.reference });
        return;
      }
      const conflict = (error as ApiError).status === 409;
      if (conflict) onConflict?.();
      notifications.show({
        color: "red",
        title: t("couldNotMarkInvoiced"),
        message: conflict ? t("expenseChangedElsewhere") : refusalMessage(error),
      });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("markInvoicedDescription")}
        </Text>
        <TextInput
          label={t("invoiceReference")}
          description={t("invoiceReferenceDescription")}
          data-autofocus
          {...form.getInputProps("reference")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("markInvoiced")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
