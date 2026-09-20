import { Button, Group, Modal, NumberInput, Select, Stack, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { createExpenseRate, type ExpenseRate, updateExpenseRate } from "../api/rates";
import { ApiValidationError } from "../api/request";
import "../i18n";
import { today } from "../lib/dates";
import { refusalMessage } from "../lib/errors";
import { useDecimalSeparator } from "../lib/format";
import { isPercentageRateKind, rateKindLabelKey, rateKinds } from "../lib/rate-kinds";

/** Adding a rate to a kind, or changing the row the caller just read off the table. */
export type RateModalState = { mode: "create"; kind: string } | { mode: "edit"; rate: ExpenseRate };

export interface RateFormModalProps {
  state: RateModalState | null;
  /** The currency a money rate starts in — the installation's own. */
  defaultCurrency: string;
  onClose: () => void;
}

const numeric = (value: number | string): number | undefined => {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
  const trimmed = value.trim().replace(",", ".");
  if (trimmed === "") return undefined;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : undefined;
};

/**
 * One dated rate. A money kind carries a currency and a value greater than
 * zero; a percentage kind carries no currency and a value between 0 and 100,
 * and sending one for the other is refused on that field. The kind is the
 * row's own on an edit — only the day, the value, the currency and the source
 * label are replaced.
 */
export const RateFormModal = ({ state, defaultCurrency, onClose }: RateFormModalProps) => {
  const { t } = useI18n("expenses");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editRateTitle") : t("addRateTitle")}
      centered
    >
      {state && (
        <RateForm
          key={state.mode === "edit" ? state.rate.id : state.kind}
          state={state}
          defaultCurrency={defaultCurrency}
          onClose={onClose}
        />
      )}
    </Modal>
  );
};

const RateForm = ({ state, defaultCurrency, onClose }: RateFormModalProps & { state: RateModalState }) => {
  const { t } = useI18n("expenses");
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();
  const editing = state.mode === "edit" ? state.rate : undefined;

  const form = useForm({
    initialValues: {
      kind: state.mode === "edit" ? state.rate.kind : state.kind,
      validFrom: (editing?.validFrom ?? today()) as string | null,
      value: (editing?.value ?? "") as number | string,
      currency: editing?.currency ?? defaultCurrency,
      source: editing?.source ?? "",
    },
    validate: {
      validFrom: (value) => (value ? null : t("validFromRequired")),
      value: (value, values) => {
        const amount = numeric(value);
        if (amount === undefined) return t("rateValueRequired");
        if (isPercentageRateKind(values.kind)) {
          return amount < 0 || amount > 100 ? t("ratePercentRange") : null;
        }
        return amount <= 0 ? t("rateAboveZero") : null;
      },
      currency: (value, values) =>
        isPercentageRateKind(values.kind) || /^[A-Za-z]{3}$/.test(value.trim()) ? null : t("currencyRequired"),
    },
  });

  const percentage = isPercentageRateKind(form.values.kind);

  const save = useMutation({
    mutationFn: (values: typeof form.values) => {
      const shared = {
        validFrom: values.validFrom ?? "",
        value: numeric(values.value) ?? 0,
        ...(percentage ? {} : { currency: values.currency.trim().toUpperCase() }),
        ...(values.source.trim() ? { source: values.source.trim() } : {}),
      };
      return editing ? updateExpenseRate(editing.id, shared) : createExpenseRate({ ...shared, kind: values.kind });
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["expenses"] });
      notifications.show({ color: "teal", title: t("rateSaved"), message: t(rateKindLabelKey(form.values.kind)) });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        if (fields.validFrom || fields.value || fields.currency || fields.kind || fields.source) {
          form.setErrors({
            validFrom: fields.validFrom,
            value: fields.value,
            currency: fields.currency,
            kind: fields.kind,
            source: fields.source,
          });
          return;
        }
      }
      notifications.show({ color: "red", title: t("couldNotSaveRate"), message: refusalMessage(error) });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Select
          label={t("rateKind")}
          withAsterisk
          disabled={editing !== undefined}
          data={rateKinds.map((kind) => ({ value: kind, label: t(rateKindLabelKey(kind)) }))}
          value={form.values.kind}
          error={form.errors.kind}
          onChange={(value) => form.setFieldValue("kind", value ?? form.values.kind)}
        />
        <DateInput
          label={t("validFrom")}
          valueFormat={t("dateInputFormat")}
          withAsterisk
          data-autofocus
          {...form.getInputProps("validFrom")}
        />
        <NumberInput
          label={percentage ? t("ratePercentValue") : t("rateValue")}
          withAsterisk
          min={0}
          max={percentage ? 100 : undefined}
          decimalScale={2}
          decimalSeparator={decimalSeparator}
          {...form.getInputProps("value")}
        />
        {!percentage && <TextInput label={t("currency")} withAsterisk {...form.getInputProps("currency")} />}
        <TextInput label={t("rateSource")} description={t("rateSourceDescription")} {...form.getInputProps("source")} />
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
