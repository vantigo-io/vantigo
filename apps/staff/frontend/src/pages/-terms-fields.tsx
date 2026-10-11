import {
  ActionIcon,
  Button,
  Card,
  Checkbox,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import type { UseFormReturnType } from "@mantine/form";
import { IconChevronDown, IconChevronRight, IconPlus, IconTrash } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { StaffAppointmentForm, StaffEmploymentType, StaffWorkingHoursScheme } from "../api/employments";
import type { StaffStructure } from "../api/meta";
import type { StaffPersonSummary } from "../api/people";
import { PersonPicker } from "../components/person-picker";
import "../i18n";
import { useDecimalSeparator, useStaffFormat } from "../lib/format";
import {
  appointmentFormsFor,
  defaultFullTimeHours,
  employeeTypes,
  employmentTypes,
  isHiredOut,
  MAX_PAY_COMPONENTS,
  noticeUnits,
  payPeriods,
  remunerationTypes,
  temporaryGrounds,
  workingHoursSchemes,
} from "../lib/terms";
import { emptyPayComponent, hoursMismatchMessage, type TermsFormValues } from "../lib/terms-form";

export interface TermsFieldsProps<V extends { terms: TermsFormValues }> {
  form: UseFormReturnType<V>;
  structure?: StaffStructure;
  /** Whether the caller writes the pay group (`canSeePay`); the pay fields are left out otherwise. */
  canSeePay: boolean;
  moreOpen: boolean;
  onToggleMore: () => void;
  /** Whether the hours were typed by hand; untouched, they follow the scheme. */
  hoursTouched: boolean;
  onHoursTouched: () => void;
  /** The manager a stored row names, so the picker says who it is before anything is searched. */
  managerSelected?: StaffPersonSummary;
}

/**
 * A terms row's fields (design §3.3, S5): the type, the percentage and the
 * hours first — the a-melding's and § 14-6's core — then where the person
 * sits and who manages them, and the rest of § 14-6 folded under "More
 * terms". The required fields are marked; the presumption that makes them
 * required is said above them by the form that composes this.
 */
export const TermsFields = <V extends { terms: TermsFormValues }>({
  form,
  structure,
  canSeePay,
  moreOpen,
  onToggleMore,
  hoursTouched,
  onHoursTouched,
  managerSelected,
}: TermsFieldsProps<V>) => {
  const { t } = useI18n("staff");
  const decimalSeparator = useDecimalSeparator();
  const format = useStaffFormat();
  // The form's values are typed by the composing form; the terms part is what this edits.
  const terms = (form.values as { terms: TermsFormValues }).terms;
  const field = (name: keyof TermsFormValues) => form.getInputProps(`terms.${name}` as never);
  const setField = (name: keyof TermsFormValues, value: unknown) =>
    form.setFieldValue(`terms.${name}` as never, value as never);

  const forms = appointmentFormsFor(terms.employmentType);
  const mismatch = hoursMismatchMessage(t, terms, format.number);
  // The rule is judged live against the scheme chosen now; a refusal the
  // server gave is shown when the form itself sees nothing wrong.
  const hoursError = mismatch ?? (form.errors["terms.fullTimeHoursPerWeek"] as string | undefined);

  // Archived rows leave the pickers.
  const departments = (structure?.departments ?? [])
    .filter((row) => !row.archivedAt)
    .map((row) => ({ value: String(row.id), label: `${row.name} (${row.code})` }));
  const locations = (structure?.locations ?? [])
    .filter((row) => !row.archivedAt)
    .map((row) => ({ value: String(row.id), label: row.name }));
  const options = <T extends string>(values: readonly T[], prefix: string) =>
    values.map((value) => ({ value, label: t(`${prefix}_${value}`) }));

  return (
    <Stack>
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <Select
          label={t("employmentType")}
          withAsterisk
          placeholder={t("stateIt")}
          data={options(employmentTypes, "employmentType")}
          {...field("employmentType")}
          onChange={(value) => {
            const type = value as StaffEmploymentType | null;
            setField("employmentType", type);
            // The appointment form follows the type when the one chosen no
            // longer goes with it, as the server's own default does.
            if (!appointmentFormsFor(type).includes(terms.appointmentForm as StaffAppointmentForm)) {
              setField("appointmentForm", type);
            }
          }}
        />
        <Select
          label={t("appointmentForm")}
          description={t("appointmentFormDescription")}
          allowDeselect={false}
          disabled={forms.length === 0}
          data={options(forms, "appointmentForm")}
          {...field("appointmentForm")}
        />
        {terms.employmentType === "midlertidig" && (
          <>
            <Select
              label={t("temporaryGround")}
              withAsterisk
              data={options(temporaryGrounds, "temporaryGround")}
              {...field("temporaryGround")}
            />
            <DateInput
              label={t("expectedEndDate")}
              valueFormat={t("dateInputFormat")}
              clearable
              {...field("expectedEndDate")}
            />
          </>
        )}
        {isHiredOut(terms.appointmentForm) && (
          <TextInput label={t("hiredOutTo")} description={t("hiredOutToDescription")} {...field("hiredOutTo")} />
        )}
        <NumberInput
          label={t("ftePercent")}
          description={t("ftePercentDescription")}
          withAsterisk
          min={0}
          max={100}
          decimalScale={2}
          decimalSeparator={decimalSeparator}
          allowNegative={false}
          {...field("ftePercent")}
        />
        <Select
          label={t("workingHoursScheme")}
          allowDeselect={false}
          data={options(workingHoursSchemes, "workingHoursScheme")}
          {...field("workingHoursScheme")}
          onChange={(value) => {
            const scheme = (value ?? "not_shift") as StaffWorkingHoursScheme;
            setField("workingHoursScheme", scheme);
            // Untouched hours follow the scheme, as the server's default does;
            // hours typed by hand stay, and the rule says if they no longer fit.
            if (!hoursTouched) setField("fullTimeHoursPerWeek", defaultFullTimeHours[scheme]);
            form.clearFieldError("terms.fullTimeHoursPerWeek" as never);
          }}
        />
        <NumberInput
          label={t("fullTimeHoursPerWeek")}
          description={t("fullTimeHoursDescription")}
          min={0}
          max={168}
          decimalScale={2}
          decimalSeparator={decimalSeparator}
          allowNegative={false}
          {...field("fullTimeHoursPerWeek")}
          onChange={(value) => {
            onHoursTouched();
            setField("fullTimeHoursPerWeek", value);
          }}
          error={hoursError}
        />
        {structure && (
          <>
            <Select label={t("department")} clearable searchable data={departments} {...field("departmentId")} />
            <Select label={t("location")} clearable searchable data={locations} {...field("locationId")} />
          </>
        )}
        <PersonPicker
          label={t("manager")}
          description={t("termsManagerDescription")}
          value={terms.managerPersonId}
          selected={managerSelected}
          onChange={(id) => setField("managerPersonId", id)}
          error={form.errors["terms.managerPersonId"] as string | undefined}
        />
        <TextInput label={t("jobTitle")} {...field("title")} />
        <TextInput
          label={t("occupationCode")}
          description={t("occupationCodeDescription")}
          inputMode="numeric"
          {...field("occupationCode")}
        />
      </SimpleGrid>

      <Button
        variant="subtle"
        justify="flex-start"
        px={0}
        leftSection={moreOpen ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
        aria-expanded={moreOpen}
        onClick={onToggleMore}
      >
        {t("moreTerms")}
      </Button>
      {moreOpen && (
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Select
            label={t("employeeType")}
            clearable
            data={options(employeeTypes, "employeeType")}
            {...field("employeeType")}
          />
          <DateInput
            label={t("probationEndDate")}
            description={t("probationEndDateDescription")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...field("probationEndDate")}
          />
          {/* An extension (aml. § 15-6 (4)) exists only as agreed in writing,
              so its day and its reason are asked for once it is. */}
          {terms.probationEndDate && (
            <Checkbox
              label={t("probationExtensionAgreed")}
              mt="lg"
              {...form.getInputProps("terms.probationExtensionAgreed" as never, { type: "checkbox" })}
            />
          )}
          {terms.probationEndDate && terms.probationExtensionAgreed && (
            <>
              <DateInput
                label={t("probationExtendedTo")}
                valueFormat={t("dateInputFormat")}
                clearable
                {...field("probationExtendedTo")}
              />
              <TextInput label={t("probationExtensionReason")} {...field("probationExtensionReason")} />
            </>
          )}
          <NumberInput
            label={t("noticeEmployee")}
            min={0}
            max={999}
            allowDecimal={false}
            {...field("noticeEmployee")}
          />
          <NumberInput
            label={t("noticeEmployer")}
            min={0}
            max={999}
            allowDecimal={false}
            {...field("noticeEmployer")}
          />
          <Select
            label={t("noticeUnit")}
            clearable
            data={options(noticeUnits, "noticeUnit")}
            {...field("noticeUnit")}
          />
          <NumberInput label={t("breakMinutes")} min={0} max={1440} allowDecimal={false} {...field("breakMinutes")} />
          <Textarea label={t("workingTimeText")} {...field("workingTimeText")} />
          <Textarea label={t("specialHoursText")} {...field("specialHoursText")} />
          <Textarea label={t("overtimeRuleText")} {...field("overtimeRuleText")} />
          <TextInput label={t("tariffReference")} {...field("tariffReference")} />
          <Textarea label={t("holidayTermsText")} {...field("holidayTermsText")} />
          <Textarea label={t("competenceDevelopmentText")} {...field("competenceDevelopmentText")} />
          <Textarea
            label={t("socialSecurityText")}
            description={t("socialSecurityTextDescription")}
            {...field("socialSecurityText")}
          />
          <TextInput label={t("costCentre")} {...field("costCentre")} />
          <TextInput label={t("division")} {...field("division")} />
          {canSeePay && (
            <>
              <Select
                label={t("remunerationType")}
                clearable
                data={options(remunerationTypes, "remunerationType")}
                {...field("remunerationType")}
              />
              <TextInput label={t("payMethod")} {...field("payMethod")} />
              <TextInput label={t("paydayText")} {...field("paydayText")} />
              <DateInput
                label={t("seniorityDate")}
                valueFormat={t("dateInputFormat")}
                clearable
                {...field("seniorityDate")}
              />
              <TextInput label={t("payGrade")} {...field("payGrade")} />
            </>
          )}
          <Textarea label={t("termsNote")} description={t("termsNoteDescription")} {...field("note")} />
        </SimpleGrid>
      )}
      {moreOpen && canSeePay && <PayComponentsField form={form} />}
    </Stack>
  );
};

/**
 * The pay components (§ 14-6 i, the pay group): a list of named amounts,
 * each with its currency and period, at most twenty. Written by those who
 * see the pay; the list is sent whole.
 */
const PayComponentsField = <V extends { terms: TermsFormValues }>({ form }: { form: UseFormReturnType<V> }) => {
  const { t } = useI18n("staff");
  const decimalSeparator = useDecimalSeparator();
  const rows = (form.values as { terms: TermsFormValues }).terms.payComponents;
  const path = (index: number, field: string) => `terms.payComponents.${index}.${field}` as never;
  return (
    <Stack gap="xs">
      <Text size="sm" fw={500}>
        {t("payComponents")}
      </Text>
      {rows.map((_, index) => (
        <Card key={index} withBorder padding="sm" radius="sm">
          <Group align="flex-start" wrap="nowrap" gap="xs">
            <SimpleGrid cols={{ base: 1, sm: 4 }} style={{ flex: 1 }}>
              <TextInput label={t("name")} withAsterisk {...form.getInputProps(path(index, "name"))} />
              <NumberInput
                label={t("amount")}
                withAsterisk
                min={0}
                decimalScale={2}
                decimalSeparator={decimalSeparator}
                allowNegative={false}
                {...form.getInputProps(path(index, "amount"))}
              />
              <TextInput
                label={t("currency")}
                withAsterisk
                maxLength={3}
                {...form.getInputProps(path(index, "currency"))}
              />
              <Select
                label={t("period")}
                allowDeselect={false}
                data={payPeriods.map((value) => ({ value, label: t(`payPeriod_${value}`) }))}
                {...form.getInputProps(path(index, "period"))}
              />
            </SimpleGrid>
            <ActionIcon
              variant="subtle"
              color="red"
              mt={28}
              aria-label={t("removePayComponent", { n: index + 1 })}
              onClick={() => form.removeListItem("terms.payComponents" as never, index)}
            >
              <IconTrash size={16} />
            </ActionIcon>
          </Group>
        </Card>
      ))}
      {rows.length < MAX_PAY_COMPONENTS ? (
        <Button
          variant="light"
          leftSection={<IconPlus size={16} />}
          style={{ alignSelf: "flex-start" }}
          onClick={() => form.insertListItem("terms.payComponents" as never, emptyPayComponent() as never)}
        >
          {t("addPayComponent")}
        </Button>
      ) : (
        <Text size="sm" c="dimmed">
          {t("payComponentsMax", { max: MAX_PAY_COMPONENTS })}
        </Text>
      )}
    </Stack>
  );
};
