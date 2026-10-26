import {
  Alert,
  Button,
  Divider,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { PlanningAllocation } from "../api/allocations";
import { ApiValidationError, isRevisionConflict, PLANNING_QUERY_KEY, refusalCode, ruleRefusals } from "../api/request";
import {
  clearAllocationTerms,
  MAX_NON_COMPETE_CUSTOMERS,
  noticeUnits,
  type PlanningCustomerRef,
  type PlanningNoticeUnit,
  type PlanningTermsInput,
  setAllocationTerms,
} from "../api/terms";
import { usePlanningFormat } from "../lib/format";
import { CustomerPicker } from "./customer-picker";

export interface TermsSectionProps {
  /** The row whose terms these are, as the sheet was opened with it. */
  allocation: PlanningAllocation;
  /** Closes the sheet: after a revision conflict, as the sheet's own form does. */
  onDone: () => void;
}

interface TermsValues {
  noticeCount: number | string;
  noticeUnit: PlanningNoticeUnit | null;
  noticeGivenOn: string | null;
  minimumUntil: string | null;
  replacementNeedsApproval: boolean;
  handoverDays: number | string;
  nonCompeteUntil: string | null;
  nonCompeteCustomers: PlanningCustomerRef[];
  reference: string;
}

const MAX_NOTICE = 365;
const MAX_HANDOVER_DAYS = 90;
const MAX_REFERENCE = 200;

const isWhole = (value: number | string, min: number, max: number) =>
  Number.isInteger(Number(value)) && Number(value) >= min && Number(value) <= max;

/**
 * An allocation's terms (design S14, §7), under the allocation sheet's form
 * in edit mode: the notice as a count and a unit and the day it was given,
 * the minimum, whether a replacement needs the customer's approval, the
 * handover days, the non-compete with its customers, and a reference. Its
 * own form and its own revision, saved whole with *Save terms* and removed
 * with *Clear terms*; the heading names the dates they derive (S15). It keeps
 * the allocation each save answers, so a second save in the same sheet sends
 * the terms' new revision. `planning:manage`'s, as the sheet is.
 */
export const TermsSection = ({ allocation, onDone }: TermsSectionProps) => {
  const [current, setCurrent] = useState(allocation);
  return (
    <TermsForm key={current.terms?.revision ?? "none"} allocation={current} onSaved={setCurrent} onDone={onDone} />
  );
};

const TermsForm = ({
  allocation,
  onSaved,
  onDone,
}: {
  allocation: PlanningAllocation;
  onSaved: (allocation: PlanningAllocation) => void;
  onDone: () => void;
}) => {
  const format = usePlanningFormat();
  const { t } = format;
  const queryClient = useQueryClient();
  const [refusal, setRefusal] = useState<string>();
  const terms = allocation.terms;

  const form = useForm<TermsValues>({
    initialValues: {
      noticeCount: terms?.noticeCount ?? "",
      noticeUnit: terms?.noticeUnit ?? null,
      noticeGivenOn: terms?.noticeGivenOn ?? null,
      minimumUntil: terms?.minimumUntil ?? null,
      replacementNeedsApproval: terms?.replacementNeedsApproval ?? false,
      handoverDays: terms?.handoverDays ?? "",
      nonCompeteUntil: terms?.nonCompeteUntil ?? null,
      nonCompeteCustomers: terms?.nonCompeteCustomers ?? [],
      reference: terms?.reference ?? "",
    },
    validate: {
      noticeCount: (value, values) =>
        value === ""
          ? values.noticeUnit
            ? t("noticeBothOrNeither")
            : null
          : isWhole(value, 1, MAX_NOTICE)
            ? null
            : t("wholeNumberBetween", { min: 1, max: MAX_NOTICE }),
      noticeUnit: (value, values) => (value === null && values.noticeCount !== "" ? t("noticeBothOrNeither") : null),
      noticeGivenOn: (value, values) =>
        value && (values.noticeCount === "" || values.noticeUnit === null) ? t("noticeGivenOnNeedsPeriod") : null,
      handoverDays: (value) =>
        value === "" || isWhole(value, 0, MAX_HANDOVER_DAYS)
          ? null
          : t("wholeNumberBetween", { min: 0, max: MAX_HANDOVER_DAYS }),
      nonCompeteUntil: (value, values) =>
        !value && values.nonCompeteCustomers.length > 0 ? t("nonCompeteNeedsDate") : null,
      nonCompeteCustomers: (value, values) =>
        values.nonCompeteUntil && value.length === 0 ? t("nonCompeteNeedsCustomers") : null,
      reference: (value) => (value.trim().length > MAX_REFERENCE ? t("textTooLong", { max: MAX_REFERENCE }) : null),
    },
  });

  /** The terms whole (S14): a field left out is cleared. */
  const inputOf = (values: TermsValues): PlanningTermsInput => ({
    ...(values.noticeCount !== "" && values.noticeUnit
      ? { noticeCount: Number(values.noticeCount), noticeUnit: values.noticeUnit }
      : {}),
    ...(values.noticeGivenOn ? { noticeGivenOn: values.noticeGivenOn } : {}),
    ...(values.minimumUntil ? { minimumUntil: values.minimumUntil } : {}),
    replacementNeedsApproval: values.replacementNeedsApproval,
    ...(values.handoverDays !== "" ? { handoverDays: Number(values.handoverDays) } : {}),
    ...(values.nonCompeteUntil
      ? {
          nonCompeteUntil: values.nonCompeteUntil,
          nonCompeteCustomerIds: values.nonCompeteCustomers.map((customer) => customer.id),
        }
      : {}),
    ...(values.reference.trim() ? { reference: values.reference.trim() } : {}),
  });

  const refused = async (error: Error) => {
    if (isRevisionConflict(error)) {
      notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
      onDone();
      return;
    }
    if (error instanceof ApiValidationError) {
      const fields = error.fieldErrors;
      const customers = Object.entries(fields).find(([field]) => field.startsWith("nonCompeteCustomerIds"))?.[1];
      form.setErrors({
        noticeCount: fields.noticeCount,
        noticeUnit: fields.noticeUnit,
        noticeGivenOn: fields.noticeGivenOn,
        minimumUntil: fields.minimumUntil,
        handoverDays: fields.handoverDays,
        nonCompeteUntil: fields.nonCompeteUntil,
        nonCompeteCustomers: customers,
        reference: fields.reference,
      });
      return;
    }
    const code = refusalCode(error);
    setRefusal(code && ruleRefusals.includes(code) ? t(`refusal.${code}`) : error.message);
  };

  const save = useMutation({
    mutationFn: (values: TermsValues) => setAllocationTerms(allocation.id, inputOf(values), terms?.revision),
    onMutate: () => setRefusal(undefined),
    onSuccess: async (answer) => {
      notifications.show({ color: "teal", title: t("termsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
      onSaved(answer);
    },
    onError: refused,
  });
  const clear = useMutation({
    mutationFn: () => clearAllocationTerms(allocation.id),
    onMutate: () => setRefusal(undefined),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("termsCleared"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
      onSaved({ ...allocation, terms: undefined, earliestRelease: allocation.toDate, handoverStart: undefined });
    },
    onError: refused,
  });

  const dateProps = {
    valueFormat: t("dateInputFormat"),
    clearable: true,
  };
  const derived = [
    t("earliestReleaseOn", { date: format.date(allocation.earliestRelease) }),
    ...(allocation.handoverStart ? [t("handoverFromOn", { date: format.date(allocation.handoverStart) })] : []),
  ].join(" · ");

  return (
    <form aria-label={t("terms")} onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Divider />
        <Stack gap={2}>
          <Title order={3} size="h4">
            {t("terms")}
          </Title>
          {terms && (
            <Text size="sm" fw={500}>
              {derived}
            </Text>
          )}
          <Text size="xs" c="dimmed">
            {t("termsDescription")}
          </Text>
        </Stack>
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("noticePeriod")}
          </Text>
          <Group grow align="flex-start" gap="xs">
            <NumberInput
              aria-label={t("noticeCount")}
              min={1}
              max={MAX_NOTICE}
              allowDecimal={false}
              allowNegative={false}
              {...form.getInputProps("noticeCount")}
            />
            <Select
              aria-label={t("noticeUnitField")}
              clearable
              data={noticeUnits.map((value) => ({ value, label: t(`noticeUnit_${value}`) }))}
              {...form.getInputProps("noticeUnit")}
            />
          </Group>
        </Stack>
        <SimpleGrid cols={{ base: 1, xs: 2 }} spacing="xs">
          <DateInput label={t("noticeGivenOn")} {...dateProps} {...form.getInputProps("noticeGivenOn")} />
          <DateInput label={t("minimumUntil")} {...dateProps} {...form.getInputProps("minimumUntil")} />
        </SimpleGrid>
        <Switch
          label={t("replacementNeedsApproval")}
          {...form.getInputProps("replacementNeedsApproval", { type: "checkbox" })}
        />
        <NumberInput
          label={t("handoverDays")}
          description={t("handoverDaysDescription")}
          min={0}
          max={MAX_HANDOVER_DAYS}
          allowDecimal={false}
          allowNegative={false}
          {...form.getInputProps("handoverDays")}
        />
        <DateInput label={t("nonCompeteUntil")} {...dateProps} {...form.getInputProps("nonCompeteUntil")} />
        <CustomerPicker
          label={t("customers")}
          description={t("customersDescription", { max: MAX_NON_COMPETE_CUSTOMERS })}
          value={form.values.nonCompeteCustomers}
          onChange={(customers) => form.setFieldValue("nonCompeteCustomers", customers)}
          max={MAX_NON_COMPETE_CUSTOMERS}
          error={form.errors.nonCompeteCustomers as string | undefined}
        />
        <TextInput
          label={t("reference")}
          description={t("referenceDescription")}
          {...form.getInputProps("reference")}
        />
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusal}
          </Alert>
        )}
        <Group justify="space-between">
          <Group gap="xs">
            {terms && (
              <Button
                variant="subtle"
                color="red"
                loading={clear.isPending}
                onClick={() =>
                  modals.openConfirmModal({
                    title: t("clearTermsTitle"),
                    children: <Text size="sm">{t("clearTermsExplained")}</Text>,
                    labels: { confirm: t("clearTerms"), cancel: t("cancel") },
                    confirmProps: { color: "red" },
                    onConfirm: () => clear.mutate(),
                  })
                }
              >
                {t("clearTerms")}
              </Button>
            )}
          </Group>
          <Button type="submit" variant="light" loading={save.isPending}>
            {t("saveTerms")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
