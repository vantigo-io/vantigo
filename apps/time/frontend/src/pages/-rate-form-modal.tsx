import { Button, Group, Modal, NumberInput, Select, Stack, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  assignableRateUsersQueryOptions,
  createPersonRate,
  type PersonRate,
  type PersonRateInput,
  updatePersonRate,
} from "../api/rates";
import { ApiValidationError } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/**
 * Adding a card, or changing the one the caller read off the table. A create
 * started from somebody's own row carries their name as well as their id: the
 * directory search need not have them on its first page, and a required field
 * must never look empty because of that.
 */
export type RateModalState =
  | { mode: "create"; userId?: string; displayName?: string }
  | { mode: "edit"; rate: PersonRate };

/** The currency a Norwegian installation writes its rates in unless it says otherwise. */
export const DEFAULT_RATE_CURRENCY = "NOK";

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

/** A rate the API would take: given, and more than zero (a plain 0 is not a rate). */
const positive = (value: number | string): boolean => {
  const given = amount(value);
  return given !== null && given > 0;
};

export interface RateFormModalProps {
  state: RateModalState | null;
  onClose: () => void;
}

/**
 * One person rate card (§4.3): the day it takes effect, a bill rate, a cost
 * rate, and the currency each is expressed in. The person is fixed once the
 * card exists — the API's update leaves them out — so the picker is only
 * offered while creating. The form lives in `RateForm`, which the modal
 * mounts fresh every time it opens.
 */
export const RateFormModal = ({ state, onClose }: RateFormModalProps) => {
  const { t } = useI18n("time");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editRateTitle") : t("addRateTitle")}
      centered
    >
      {state && <RateForm state={state} onClose={onClose} />}
    </Modal>
  );
};

const RateForm = ({ state, onClose }: RateFormModalProps & { state: RateModalState }) => {
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
      // §4.3: a card has to price something, and a rate it gives is more than
      // zero. The "one of the two" message names both rates and sits on the
      // first of them, rather than being said twice.
      billRate: (value, values) => {
        if (amount(value) === null) return amount(values.costRate) === null ? t("rateRequired") : null;
        return positive(value) ? null : t("rateAboveZero");
      },
      costRate: (value) => (amount(value) === null || positive(value) ? null : t("rateAboveZero")),
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

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        {rate ? (
          <TextInput label={t("person")} value={rate.displayName} readOnly />
        ) : (
          <PersonPicker
            value={form.values.userId}
            prefilled={
              state.mode === "create" && state.userId && state.displayName
                ? { value: state.userId, label: state.displayName }
                : undefined
            }
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
          {/* `min` keeps a negative amount out; zero is typed like any other
              number and refused in words, so the rule is never a silent clamp. */}
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

/**
 * The person a new card is for, searched straight from the API's own window
 * onto the user directory — so a new hire with nothing logged yet can be given
 * a rate before their first entry. The server has already narrowed the list
 * and answers at most twenty, so Mantine's own filtering is switched off; the
 * chosen person is kept in the list even once the search has moved past them,
 * and a person the form was opened for is pinned from the very first render.
 */
const PersonPicker = ({
  value,
  prefilled,
  onChange,
  error,
}: {
  value: string | null;
  /** The person the form was opened for, named, so the field is never blank. */
  prefilled?: { value: string; label: string };
  onChange: (value: string | null) => void;
  error?: ReactNode;
}) => {
  const { t } = useI18n("time");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const { data } = useQuery(assignableRateUsersQueryOptions(debouncedSearch));
  const [chosen, setChosen] = useState<{ value: string; label: string } | null>(prefilled ?? null);

  const users = data ?? [];
  const options = users.map((user) => ({ value: user.userId, label: user.displayName }));
  if (chosen && !users.some((user) => user.userId === chosen.value)) options.push(chosen);

  return (
    <Select
      label={t("person")}
      placeholder={t("choosePerson")}
      description={t("ratePersonDescription")}
      withAsterisk
      searchable
      data-autofocus
      // The API has already matched on the term; filtering again would hide a
      // person whose name does not contain it the way the client compares.
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noPeopleForRates")}
      data={options}
      value={value}
      onChange={(next) => {
        const picked = options.find((option) => option.value === next);
        setChosen(picked ?? null);
        onChange(next);
      }}
      error={error}
    />
  );
};
