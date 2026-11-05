import { Group, NumberInput, SegmentedControl, Select, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import type { FinalPeriod, SubscriptionBilling, SubscriptionInterval } from "../api/types";
import { useOrdersFormat } from "../lib/format";
import { FINAL_PERIODS, INTERVALS, type PlanState } from "../lib/plan";

/**
 * A subscription's plan (O42): the interval, billing in advance or in
 * arrears, the start, an optional end, the notice in months, the invoice
 * day and how the period an end falls in is billed — in full or pro-rated to
 * the end. Periods run from the first of the start's month; a start on
 * another day makes the first period partial and pro-rated. The final period
 * stays editable on an activated subscription (`finalPeriodEditable`); left
 * empty on a new one, it is the settings' default.
 */
export const PlanFields = ({
  plan,
  onChange,
  disabled = false,
  finalPeriodEditable = !disabled,
  errors = {},
}: {
  plan: PlanState;
  onChange: (patch: Partial<PlanState>) => void;
  disabled?: boolean;
  finalPeriodEditable?: boolean;
  errors?: Record<string, string>;
}) => {
  const { t } = useOrdersFormat();
  return (
    <Stack gap="sm">
      <Group align="flex-end" wrap="wrap">
        <Select
          label={t("intervalLabel")}
          w={170}
          allowDeselect={false}
          disabled={disabled}
          data={INTERVALS.map((value) => ({ value, label: t(`interval.${value}`) }))}
          value={plan.interval}
          onChange={(value) => value && onChange({ interval: value as SubscriptionInterval })}
          error={errors.interval}
        />
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("billingLabel")}
          </Text>
          <SegmentedControl
            aria-label={t("billingLabel")}
            disabled={disabled}
            value={plan.billing}
            onChange={(value) => onChange({ billing: value as SubscriptionBilling })}
            data={[
              { value: "in_advance", label: t("billing.in_advance") },
              { value: "in_arrears", label: t("billing.in_arrears") },
            ]}
          />
        </Stack>
        <DateInput
          label={t("startDate")}
          w={170}
          required
          valueFormat={t("dateInputFormat")}
          disabled={disabled}
          value={plan.startDate}
          onChange={(value) => onChange({ startDate: value ?? null })}
          error={errors.startDate}
        />
        <DateInput
          label={t("endDate")}
          w={170}
          clearable
          valueFormat={t("dateInputFormat")}
          disabled={disabled}
          value={plan.endDate}
          onChange={(value) => onChange({ endDate: value ?? null })}
          error={errors.endDate}
        />
        <NumberInput
          label={t("noticeMonths")}
          w={140}
          min={0}
          max={12}
          allowDecimal={false}
          disabled={disabled}
          value={plan.noticeMonths}
          onChange={(value) => onChange({ noticeMonths: value === "" ? "" : Number(value) })}
          error={errors.noticeMonths}
        />
        <NumberInput
          label={t("invoiceDay")}
          description={t("invoiceDayHint")}
          w={160}
          min={1}
          max={28}
          allowDecimal={false}
          disabled={disabled}
          value={plan.invoiceDay}
          onChange={(value) => onChange({ invoiceDay: value === "" ? "" : Number(value) })}
          error={errors.invoiceDay}
        />
        <Select
          label={t("finalPeriodLabel")}
          w={220}
          clearable={plan.finalPeriod === null}
          placeholder={t("finalPeriodFromSettings")}
          disabled={!finalPeriodEditable}
          data={FINAL_PERIODS.map((value) => ({ value, label: t(`finalPeriod.${value}`) }))}
          value={plan.finalPeriod}
          onChange={(value) => onChange({ finalPeriod: (value as FinalPeriod | null) ?? null })}
          error={errors.finalPeriod}
          data-assist="orders.subscription.final-period"
        />
      </Group>
      {plan.startDate && !plan.startDate.endsWith("-01") && (
        <Text size="xs" c="dimmed">
          {t("partialFirstPeriod")}
        </Text>
      )}
    </Stack>
  );
};
