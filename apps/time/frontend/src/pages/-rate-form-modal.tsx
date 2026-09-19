import { Button, Group, Modal, NumberInput, Select, Stack, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { createPersonRate, type PersonRate, type PersonRateInput, updatePersonRate } from "../api/rates";
import { ApiValidationError } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";

/** Adding a card, or changing the one the caller read off the table. */
export type RateModalState = { mode: "create"; userId?: string } | { mode: "edit"; rate: PersonRate };

/** The currency a Norwegian installation writes its rates in unless it says otherwise. */
export const DEFAULT_RATE_CURRENCY = "NOK";

/** Somebody a rate card can be written for, as the settings page works them out. */
export interface RatePerson {
  userId: string;
  displayName: string;
}

/** The request fields a refusal may name that this form has an input for. */
const formFields = new Set(["userId", "validFrom", "billRate", "costRate", "currency"]);

interface RateFormValues {
  userId: string | null;
  validFrom: string | null;
  billRate: number | string;
  costRate: number | string;
  currency: string;
}

const amount = (value: number | string): number | null => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? null : Number(trimmed);
};

export interface RateFormModalProps {
  state: RateModalState | null;
  /** Everyone a card may be written for: the people with time, plus everyone who already has one. */
  people: RatePerson[];
  onClose: () => void;
}

/**
 * One person rate card (§4.3): the day it takes effect, a bill rate, a cost
 * rate, and the currency each is expressed in. The person is fixed once the
 * card exists — the API's update leaves them out — so the picker is only
 * offered while creating. The form lives in `RateForm`, which the modal
 * mounts fresh every time it opens.
 */
export const RateFormModal = ({ state, people, onClose }: RateFormModalProps) => {
  const { t } = useI18n("time");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editRateTitle") : t("addRateTitle")}
      centered
    >
      {state && <RateForm state={state} people={people} onClose={onClose} />}
    </Modal>
  );
};

const RateForm = ({ state, people, onClose }: RateFormModalProps & { state: RateModalState }) => {
  const { t } = useI18n("time");
  const queryClient = useQueryClient();
  const rate = state.mode === "edit" ? state.rate : undefined;

  const form = useForm<RateFormValues>({
    initialValues: {
      userId: rate?.userId ?? (state.mode === "create" ? (state.userId ?? null) : null),
      validFrom: rate?.validFrom ?? null,
      billRate: rate?.billRate ?? "",
      costRate: rate?.costRate ?? "",
      currency: rate?.currency ?? DEFAULT_RATE_CURRENCY,
    },
    validate: {
      userId: (value) => (value ? null : t("personRequired")),
      validFrom: (value) => (value ? null : t("validFromRequired")),
      // §4.3: a card has to price something. The message names both rates and
      // sits on the first of the two, rather than being said twice.
      billRate: (value, values) =>
        amount(value) === null && amount(values.costRate) === null ? t("rateRequired") : null,
      currency: (value) => (/^[A-Za-z]{3}$/.test(value.trim()) ? null : t("currencyRequired")),
    },
  });

  const save = useMutation({
    mutationFn: (values: RateFormValues) => {
      const body = {
        validFrom: values.validFrom ?? "",
        billRate: amount(values.billRate),
        costRate: amount(values.costRate),
        currency: values.currency.trim().toUpperCase(),
      };
      if (rate) return updatePersonRate(rate.id, body);
      return createPersonRate({ userId: values.userId ?? "", ...body } satisfies PersonRateInput);
    },
    onSuccess: async (saved) => {
      notifications.show({ color: "teal", title: t("rateSaved"), message: saved.displayName });
      onClose();
      await queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) => {
      if (failure instanceof ApiValidationError) {
        const fields = Object.fromEntries(
          Object.entries(failure.fieldErrors).filter(([field]) => formFields.has(field)),
        );
        if (Object.keys(fields).length > 0) {
          form.setErrors(fields);
          return;
        }
      }
      notifications.show({ color: "red", title: t("couldNotSaveRate"), message: refusalMessage(failure, "validFrom") });
    },
  });

  const options = people.map((person) => ({ value: person.userId, label: person.displayName }));

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        {rate ? (
          <TextInput label={t("person")} value={rate.displayName} readOnly />
        ) : (
          <Select
            label={t("person")}
            placeholder={t("choosePerson")}
            description={t("ratePersonDescription")}
            withAsterisk
            searchable
            data-autofocus
            nothingFoundMessage={t("noPeopleForRates")}
            data={options}
            value={form.values.userId}
            onChange={(value) => form.setFieldValue("userId", value)}
            error={form.errors.userId}
          />
        )}
        <DateInput
          label={t("validFrom")}
          valueFormat={t("dateInputFormat")}
          withAsterisk
          {...form.getInputProps("validFrom")}
        />
        <Group grow align="start">
          <NumberInput label={t("billRate")} min={0} decimalScale={2} {...form.getInputProps("billRate")} />
          <NumberInput label={t("costRate")} min={0} decimalScale={2} {...form.getInputProps("costRate")} />
        </Group>
        <TextInput
          label={t("currency")}
          withAsterisk
          {...form.getInputProps("currency")}
          onChange={(event) => form.setFieldValue("currency", event.currentTarget.value.toUpperCase())}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
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
