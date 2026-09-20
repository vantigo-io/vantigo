import { Button, Group, Modal, NumberInput, Select, Stack, Switch, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type BillingInput, setExpenseBilling } from "../api/approvals";
import type { Expense } from "../api/entries";
import { expenseProjectsQueryOptions } from "../api/projects";
import { type ApiError, ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useDecimalSeparator, useExpenseFormat } from "../lib/format";

export interface BillingModalProps {
  /** The expense being priced from its project's side, or null when closed. */
  expense: Expense | null;
  /** The revision the drawer was opened at. */
  revision: number | undefined;
  onClose: () => void;
  onSaved: (expense: Expense) => void;
}

const numeric = (value: number | string): number | undefined => {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
  const trimmed = value.trim().replace(",", ".");
  if (trimmed === "") return undefined;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : undefined;
};

/**
 * What an expense bills its customer, set from the project's side
 * (decision X7). This is a door of its own: `expenses:manage` does not imply
 * financial rights on a project, an employee's expense form never carries a
 * markup, and the period lock does not reach it — pricing is bookkeeping done
 * after a period closes.
 *
 * A figure left empty is **left out of the request**, which keeps whatever
 * the line already carries; a line that carries none takes the settings'
 * default markup or the `mileage_customer` rate in force on its date.
 */
export const BillingModal = ({ expense, revision, onClose, onSaved }: BillingModalProps) => {
  const { t } = useI18n("expenses");
  return (
    <Modal opened={expense !== null} onClose={onClose} title={t("setBillingTitle")} centered>
      {expense && (
        <BillingForm key={expense.id} expense={expense} revision={revision} onClose={onClose} onSaved={onSaved} />
      )}
    </Modal>
  );
};

const BillingForm = ({ expense, revision, onClose, onSaved }: BillingModalProps & { expense: Expense }) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();
  const { data: projects } = useQuery(expenseProjectsQueryOptions());

  const mileage = expense.kind === "mileage";
  const lines = projects?.find((project) => project.id === expense.project?.id)?.billingLines ?? [];

  const form = useForm({
    initialValues: {
      billable: expense.billable,
      billingLineId: expense.billingLine ? String(expense.billingLine.id) : null,
      markupPercent: (expense.billing?.markupPercent ?? "") as number | string,
      billRatePerKm: (expense.billing?.billRatePerKm ?? "") as number | string,
    },
  });

  const save = useMutation({
    mutationFn: (values: typeof form.values) => {
      const markup = numeric(values.markupPercent);
      const perKm = numeric(values.billRatePerKm);
      const input: BillingInput = {
        billable: values.billable,
        revision: revision ?? expense.revision,
        ...(values.billingLineId ? { billingLineId: Number(values.billingLineId) } : {}),
        ...(values.billable && !mileage && markup !== undefined ? { markupPercent: markup } : {}),
        ...(values.billable && mileage && perKm !== undefined ? { billRatePerKm: perKm } : {}),
      };
      return setExpenseBilling(expense.id, input);
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({
        color: "teal",
        title: t("billingSaved"),
        message: t("billAmountIsNow", {
          amount: format.money(saved.billing?.billAmount ?? 0, saved.currency),
        }),
      });
      onSaved(saved);
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        if (fields.markupPercent || fields.billRatePerKm || fields.billingLineId || fields.billable) {
          form.setErrors({
            markupPercent: fields.markupPercent,
            billRatePerKm: fields.billRatePerKm,
            billingLineId: fields.billingLineId,
            billable: fields.billable,
          });
          return;
        }
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotSaveBilling"),
        message: conflict ? t("expenseChangedElsewhere") : refusalMessage(error),
      });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("setBillingDescription")}
        </Text>
        <Switch label={t("billable")} {...form.getInputProps("billable", { type: "checkbox" })} />
        <Select
          label={t("billingLine")}
          placeholder={t("noBillingLine")}
          clearable
          disabled={lines.length === 0}
          data={lines.map((line) => ({ value: String(line.id), label: line.code }))}
          {...form.getInputProps("billingLineId")}
        />
        {form.values.billable &&
          (mileage ? (
            <NumberInput
              label={t("billRatePerKm")}
              description={t("keptWhenLeftEmpty")}
              min={0}
              decimalScale={2}
              decimalSeparator={decimalSeparator}
              {...form.getInputProps("billRatePerKm")}
            />
          ) : (
            <NumberInput
              label={t("markupPercent")}
              description={t("keptWhenLeftEmpty")}
              min={0}
              max={1000}
              decimalScale={2}
              decimalSeparator={decimalSeparator}
              {...form.getInputProps("markupPercent")}
            />
          ))}
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
