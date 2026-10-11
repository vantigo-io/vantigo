import { Button, Select, SimpleGrid, Stack, Switch, Text, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import type { UseFormReturnType } from "@mantine/form";
import { IconChevronDown, IconChevronRight } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { EMPLOYEE_NUMBER_MAX_LENGTH } from "../lib/employee-number";
import type { PersonFormValues } from "../lib/person-form";
import { genders } from "../lib/terms";

export interface PersonFieldsProps {
  form: UseFormReturnType<PersonFormValues>;
  /** What the generated number will be, when the settings could be read. */
  generatedNumber?: string;
  /** The number of a person who exists: shown, never typed — it does not change after the create. */
  fixedNumber?: string;
  privateOpen: boolean;
  onTogglePrivate: () => void;
}

/**
 * A person's fields: the work group first — the names, the employee number,
 * how to reach them at work and their language — and the private group
 * folded away, optional, since a hire is often entered before HR has it.
 */
export const PersonFields = ({
  form,
  generatedNumber,
  fixedNumber,
  privateOpen,
  onTogglePrivate,
}: PersonFieldsProps) => {
  const { t } = useI18n("staff");
  return (
    <Stack>
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <TextInput label={t("givenName")} withAsterisk data-autofocus {...form.getInputProps("givenName")} />
        <TextInput label={t("familyName")} withAsterisk {...form.getInputProps("familyName")} />
      </SimpleGrid>
      <TextInput
        label={t("displayName")}
        description={t("displayNameDescription")}
        {...form.getInputProps("displayName")}
      />
      {fixedNumber !== undefined ? (
        <Text size="sm" c="dimmed">
          {t("employeeNumberFixed", { number: fixedNumber })}
        </Text>
      ) : (
        <Stack gap={4}>
          <Switch
            label={t("assignOwnEmployeeNumber")}
            {...form.getInputProps("assignOwnNumber", { type: "checkbox" })}
          />
          {form.values.assignOwnNumber ? (
            <TextInput
              label={t("employeeNumber")}
              description={t("employeeNumberDescription", { max: EMPLOYEE_NUMBER_MAX_LENGTH })}
              withAsterisk
              {...form.getInputProps("employeeNumber")}
            />
          ) : (
            <Text size="sm" c="dimmed">
              {generatedNumber
                ? t("employeeNumberGenerated", { number: generatedNumber })
                : t("employeeNumberGeneratedPlain")}
            </Text>
          )}
          {/* A refusal of the generated number (employee_number_too_long) has no
            field on screen to land on while the switch is off. */}
          {!form.values.assignOwnNumber && form.errors.employeeNumber && (
            <Text size="sm" c="red">
              {form.errors.employeeNumber}
            </Text>
          )}
        </Stack>
      )}
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <TextInput label={t("workEmail")} type="email" {...form.getInputProps("workEmail")} />
        <TextInput label={t("phoneWork")} {...form.getInputProps("phoneWork")} />
        <Select
          label={t("preferredLanguage")}
          allowDeselect={false}
          data={[
            { value: "nb", label: t("languageNb") },
            { value: "en", label: t("languageEn") },
          ]}
          {...form.getInputProps("preferredLanguage")}
        />
        <TextInput
          label={t("workCountry")}
          description={t("workCountryDescription")}
          {...form.getInputProps("workCountry")}
        />
      </SimpleGrid>

      <Button
        variant="subtle"
        justify="flex-start"
        px={0}
        leftSection={privateOpen ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
        aria-expanded={privateOpen}
        onClick={onTogglePrivate}
      >
        {t("privateDetails")}
      </Button>
      {/* Rendered only while open: folded fields are not in the way of a keyboard
          user tabbing to the buttons. */}
      {privateOpen && (
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <DateInput
            label={t("birthDate")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("birthDate")}
          />
          <Select
            label={t("gender")}
            clearable
            data={genders.map((value) => ({
              value,
              label: t(`gender_${value}`),
            }))}
            {...form.getInputProps("gender")}
          />
          <TextInput label={t("phoneMobile")} {...form.getInputProps("phoneMobile")} />
          <TextInput label={t("phonePrivate")} {...form.getInputProps("phonePrivate")} />
          <TextInput label={t("privateEmail")} type="email" {...form.getInputProps("privateEmail")} />
          <TextInput label={t("street")} {...form.getInputProps("street")} />
          <TextInput label={t("postalCode")} {...form.getInputProps("postalCode")} />
          <TextInput label={t("city")} {...form.getInputProps("city")} />
          <TextInput
            label={t("country")}
            description={t("homeCountryDescription")}
            {...form.getInputProps("country")}
          />
        </SimpleGrid>
      )}
    </Stack>
  );
};
