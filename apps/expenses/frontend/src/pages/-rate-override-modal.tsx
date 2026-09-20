import { Button, Group, Modal, NumberInput, Stack, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { overrideExpenseRate } from "../api/approvals";
import type { Expense } from "../api/entries";
import { type ApiError, ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useDecimalSeparator, useExpenseFormat } from "../lib/format";

export interface RateOverrideModalProps {
  /** The submitted mileage line being repriced, or null when the modal is closed. */
  expense: Expense | null;
  /** The revision the drawer was opened at; a revision that has moved on is a 409. */
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
 * Replacing the rate the table gave a submitted mileage line (decision X8).
 *
 * The dialog says what the table had said and what it will become, because
 * that is the whole decision: `rateOverride.tableValue` once somebody has
 * already replaced it, and the line's own frozen rate before that — the
 * server records the table's figure the first time and no later, so the
 * original is never lost behind a second override.
 */
export const RateOverrideModal = ({ expense, revision, onClose, onSaved }: RateOverrideModalProps) => {
  const { t } = useI18n("expenses");
  return (
    <Modal opened={expense !== null} onClose={onClose} title={t("overrideRateTitle")} centered>
      {expense && (
        <RateOverrideForm key={expense.id} expense={expense} revision={revision} onClose={onClose} onSaved={onSaved} />
      )}
    </Modal>
  );
};

const RateOverrideForm = ({ expense, revision, onClose, onSaved }: RateOverrideModalProps & { expense: Expense }) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();

  const passengers = expense.passengers ?? 0;
  const tableRate = expense.rateOverride?.tableValue ?? expense.rate;
  const tablePassengerRate = expense.rateOverride?.passengerTableValue ?? expense.passengerRate;

  const form = useForm({
    initialValues: {
      rate: (expense.rate ?? "") as number | string,
      passengerRate: (expense.passengerRate ?? "") as number | string,
    },
    validate: {
      rate: (value) => {
        const rate = numeric(value);
        return rate === undefined || rate <= 0 ? t("rateAboveZero") : null;
      },
      passengerRate: (value) => {
        if (passengers === 0) return null;
        const rate = numeric(value);
        return rate !== undefined && rate < 0 ? t("passengerRateNotBelowZero") : null;
      },
    },
  });

  const save = useMutation({
    mutationFn: (values: { rate: number | string; passengerRate: number | string }) => {
      const passengerRate = numeric(values.passengerRate);
      return overrideExpenseRate(expense.id, {
        rate: numeric(values.rate) ?? 0,
        revision: revision ?? expense.revision,
        ...(passengers > 0 && passengerRate !== undefined ? { passengerRate } : {}),
      });
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({
        color: "teal",
        title: t("rateOverridden"),
        message: format.money(saved.grossAmount, saved.currency),
      });
      onSaved(saved);
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        if (fields.rate || fields.passengerRate) {
          form.setErrors({ rate: fields.rate, passengerRate: fields.passengerRate });
          return;
        }
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotOverrideRate"),
        message: conflict ? t("expenseChangedElsewhere") : refusalMessage(error),
      });
    },
  });

  const newRate = numeric(form.values.rate);
  const newPassengerRate = numeric(form.values.passengerRate);

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm" data-testid="rate-change">
          {t("tableValueToNew", {
            table: tableRate === undefined ? t("notAvailable") : format.money(tableRate, expense.currency),
            next: newRate === undefined ? t("notAvailable") : format.money(newRate, expense.currency),
          })}
        </Text>
        <NumberInput
          label={t("ratePerKm")}
          withAsterisk
          min={0}
          decimalScale={2}
          decimalSeparator={decimalSeparator}
          data-autofocus
          {...form.getInputProps("rate")}
        />
        {passengers > 0 && (
          <>
            <Text size="sm" data-testid="passenger-rate-change">
              {t("passengerTableValueToNew", {
                table:
                  tablePassengerRate === undefined
                    ? t("notAvailable")
                    : format.money(tablePassengerRate, expense.currency),
                next:
                  newPassengerRate === undefined ? t("notAvailable") : format.money(newPassengerRate, expense.currency),
              })}
            </Text>
            <NumberInput
              label={t("passengerRatePerKm")}
              description={t("passengersOnThisLine", { count: passengers })}
              min={0}
              decimalScale={2}
              decimalSeparator={decimalSeparator}
              {...form.getInputProps("passengerRate")}
            />
          </>
        )}
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
