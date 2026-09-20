import { Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { FlowUnits } from "../api/entries";
import { markUnitsReimbursed } from "../api/reimbursements";
import { ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { today } from "../lib/dates";
import { refusalMessages } from "../lib/errors";

/** What the contract allows in a payroll reference, once trimmed. */
export const REFERENCE_MAX_LENGTH = 100;

export interface MarkReimbursedModalProps {
  /**
   * The units one payroll run pays for — standalone expenses, whole travel
   * claims or both — or null when the modal is closed.
   */
  units: FlowUnits | null;
  onClose: () => void;
  onDone: () => void;
}

/**
 * One payroll run, recorded with the day it was made and the reference
 * whoever made it can find it by. All or nothing, exactly as the flow's
 * batches are: a unit that is not approved, owes the employee nothing, or
 * has been paid already refuses the whole request and nothing is stamped. A
 * trip is paid as one unit, for the sum of what its lines owe.
 *
 * The date is today by default and can never be in the future — this records
 * a payment that happened, not one somebody means to make.
 */
export const MarkReimbursedModal = ({ units, onClose, onDone }: MarkReimbursedModalProps) => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  const [refusals, setRefusals] = useState<string[]>([]);
  const now = today();

  const form = useForm({
    initialValues: { date: now as string | null, reference: "" },
    validate: {
      date: (value) => {
        if (!value) return t("payoutDateRequired");
        return value > now ? t("payoutDateNotInFuture") : null;
      },
      reference: (value) => (value.trim().length > REFERENCE_MAX_LENGTH ? t("referenceTooLong") : null),
    },
  });

  const mark = useMutation({
    mutationFn: (values: { date: string | null; reference: string }) =>
      markUnitsReimbursed({
        ...(units ?? {}),
        date: values.date ?? now,
        ...(values.reference.trim() ? { reference: values.reference.trim() } : {}),
      }),
    onSuccess: async (paid) => {
      setRefusals([]);
      form.reset();
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      const count = paid.entries.length + paid.claims.length;
      notifications.show({
        color: "teal",
        title: t("markedReimbursed"),
        message: count === 1 ? t("oneExpense") : t("countOfExpenses", { count }),
      });
      onDone();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        if (fields.date || fields.reference) {
          form.setErrors({ date: fields.date, reference: fields.reference });
          return;
        }
      }
      setRefusals(refusalMessages(error));
    },
  });

  return (
    <Modal
      opened={units !== null}
      onClose={onClose}
      title={t("markReimbursedTitle")}
      centered
      onExitTransitionEnd={() => setRefusals([])}
    >
      <form onSubmit={form.onSubmit((values) => mark.mutate(values))}>
        <Stack>
          <Text size="sm" c="dimmed">
            {t("markReimbursedDescription", {
              count: (units?.entryIds?.length ?? 0) + (units?.claimIds?.length ?? 0),
            })}
          </Text>
          <RefusalList messages={refusals} />
          <DateInput
            label={t("payoutDate")}
            valueFormat={t("dateInputFormat")}
            withAsterisk
            maxDate={now}
            data-autofocus
            {...form.getInputProps("date")}
          />
          <TextInput
            label={t("payrollReference")}
            description={t("payrollReferenceDescription")}
            {...form.getInputProps("reference")}
          />
          <Group justify="flex-end">
            <Button type="button" variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mark.isPending}>
              {t("markReimbursed")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
