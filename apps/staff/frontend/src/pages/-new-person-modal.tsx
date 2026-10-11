import {
  Alert,
  Button,
  Group,
  Modal,
  Select,
  SimpleGrid,
  Stack,
  Stepper,
  Switch,
  Text,
  TextInput,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { createEmployment, type StaffRelationshipType } from "../api/employments";
import { staffMetaQueryOptions } from "../api/meta";
import { createPerson, type StaffPerson } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import { staffSettingsQueryOptions } from "../api/settings";
import "../i18n";
import { NUMBER_CHARACTERS, nextGeneratedNumber } from "../lib/employee-number";
import { fieldRefusals } from "../lib/errors";
import { useStaffFormat } from "../lib/format";
import {
  emptyPerson,
  type PersonFormValues,
  personFields,
  personInputOf,
  personValidation,
  privateFields,
} from "../lib/person-form";
import { relationshipTypes } from "../lib/terms";
import {
  emptyTerms,
  moreTermsFields,
  type TermsFormValues,
  termsFields,
  termsInputOf,
  termsValidation,
} from "../lib/terms-form";
import { PersonFields } from "./-person-form";
import { TermsFields } from "./-terms-fields";

export interface NewPersonModalProps {
  opened: boolean;
  onClose: () => void;
  /** Where the person is shown once they exist — their page, by the host's route. */
  onCreated: (personId: number) => void;
}

/**
 * New person (design §7): two steps, as the API has them — the person, then
 * their first employment with its first terms. The person is saved by the
 * first step, so a hire whose terms are not settled yet still exists and
 * stays findable (People's `current` lists "no employment"), and the second
 * step can be done later from the person's page.
 *
 * On a phone the form takes the whole screen.
 */
export const NewPersonModal = ({ opened, onClose, onCreated }: NewPersonModalProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  const [person, setPerson] = useState<StaffPerson | null>(null);

  const close = () => {
    setPerson(null);
    onClose();
  };
  const done = (id: number) => {
    setPerson(null);
    onCreated(id);
  };

  return (
    <Modal opened={opened} onClose={close} title={t("newPerson")} size="xl" fullScreen={phone}>
      {opened && (
        <Stepper active={person ? 1 : 0} allowNextStepsSelect={false} size="sm">
          <Stepper.Step label={t("stepPerson")} description={t("stepPersonDescription")}>
            <PersonStep onSaved={setPerson} onCancel={close} />
          </Stepper.Step>
          <Stepper.Step label={t("stepEmployment")} description={t("stepEmploymentDescription")}>
            {person && <EmploymentStep person={person} onDone={() => done(person.id)} />}
          </Stepper.Step>
        </Stepper>
      )}
    </Modal>
  );
};

const PersonStep = ({ onSaved, onCancel }: { onSaved: (person: StaffPerson) => void; onCancel: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  // The settings are staff:manage's, as this form is; read only to say which
  // number comes next, so a failed read just says less.
  const { data: settings } = useQuery(staffSettingsQueryOptions());
  const [privateOpen, setPrivateOpen] = useState(false);
  const form = useForm<PersonFormValues>({ initialValues: emptyPerson(), validate: personValidation(t) });

  const save = useMutation({
    mutationFn: (values: PersonFormValues) => createPerson(personInputOf(values)),
    onSuccess: async (created) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onSaved(created);
    },
    onError: (error) => {
      const { fields, rest } = fieldRefusals(error, personFields);
      form.setErrors(fields);
      if (Object.keys(fields).some((field) => (privateFields as readonly string[]).includes(field)))
        setPrivateOpen(true);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSavePerson"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack mt="md">
        <PersonFields
          form={form}
          generatedNumber={
            settings ? nextGeneratedNumber(settings.employeeNumberPrefix, settings.nextEmployeeNumber) : undefined
          }
          privateOpen={privateOpen}
          onTogglePrivate={() => setPrivateOpen((open) => !open)}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onCancel}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("saveAndContinue")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

interface EmploymentFormValues {
  businessUnitId: string | null;
  startDate: string | null;
  assignOwnNumber: boolean;
  employmentNumber: string;
  relationshipType: StaffRelationshipType | null;
  contractSignedAt: string | null;
  terms: TermsFormValues;
}

/** What "More terms" folds away: the rest of the terms, and the employment's own two rarely-set fields. */
const foldedFields: readonly string[] = [...moreTermsFields, "relationshipType", "contractSignedAt"];

const EmploymentStep = ({ person, onDone }: { person: StaffPerson; onDone: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const format = useStaffFormat();
  const { data: meta } = useQuery(staffMetaQueryOptions());
  const [moreOpen, setMoreOpen] = useState(false);
  const [hoursTouched, setHoursTouched] = useState(false);
  const structure = meta?.structure;
  // Archived units leave the picker; the default is where an employment goes when it names none.
  const units = (structure?.businessUnits ?? []).filter((unit) => !unit.archivedAt);
  const defaultUnit = units.find((unit) => unit.isDefault);

  const form = useForm<EmploymentFormValues>({
    initialValues: {
      businessUnitId: defaultUnit ? String(defaultUnit.id) : null,
      // The business zone's today, as the server judges it — never the browser's.
      startDate: meta?.today ?? null,
      assignOwnNumber: false,
      employmentNumber: "",
      relationshipType: null,
      contractSignedAt: null,
      terms: emptyTerms(),
    },
    validate: {
      startDate: (value) => (value ? null : t("startDateRequired")),
      employmentNumber: (value, values) => {
        if (!values.assignOwnNumber) return null;
        if (!value.trim()) return t("employmentNumberRequired");
        return value.trim().length > 40 || !NUMBER_CHARACTERS.test(value.trim()) ? t("employmentNumberInvalid") : null;
      },
      terms: termsValidation(t, format.number),
    },
  });

  const save = useMutation({
    mutationFn: (values: EmploymentFormValues) =>
      createEmployment(person.id, {
        ...(values.businessUnitId ? { businessUnitId: Number(values.businessUnitId) } : {}),
        startDate: values.startDate as string,
        ...(values.assignOwnNumber && values.employmentNumber.trim()
          ? { employmentNumber: values.employmentNumber.trim() }
          : {}),
        ...(values.relationshipType ? { relationshipType: values.relationshipType } : {}),
        ...(values.contractSignedAt ? { contractSignedAt: values.contractSignedAt } : {}),
        terms: termsInputOf(values.terms),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("personCreated", { name: person.displayName }), message: "" });
      onDone();
    },
    onError: (error) => {
      // A field error lands on a field the form can show: the folded ones
      // unfold for it, and one the form never renders (the structure
      // pickers without a structure) is said out loud instead.
      const shown = [
        ...(structure ? ["businessUnitId"] : []),
        "startDate",
        "employmentNumber",
        "relationshipType",
        "contractSignedAt",
        ...termsFields
          .filter((field) => structure || (field !== "departmentId" && field !== "locationId"))
          .map((field) => `terms.${field}`),
      ];
      const { fields, rest } = fieldRefusals(error, shown);
      form.setErrors(fields);
      // The typed-number field exists only while the switch is on.
      if (fields.employmentNumber) form.setFieldValue("assignOwnNumber", true);
      if (Object.keys(fields).some((field) => foldedFields.includes(field.replace(/^terms\./, "")))) setMoreOpen(true);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveEmployment"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack mt="md">
        <Text size="sm">{t("personSaved", { name: person.displayName, number: person.employeeNumber })}</Text>
        {/* Not a colour alone and not a tooltip: the reason the two fields
            are required is the law's, and it is said in words. */}
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("presumptionTitle")}>
          {t("presumptionWarning")}
        </Alert>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          {structure && (
            <Select
              label={t("businessUnit")}
              description={t("businessUnitDescription")}
              allowDeselect={false}
              data={units.map((unit) => ({ value: String(unit.id), label: unit.name }))}
              {...form.getInputProps("businessUnitId")}
            />
          )}
          <DateInput
            label={t("startDate")}
            withAsterisk
            valueFormat={t("dateInputFormat")}
            {...form.getInputProps("startDate")}
          />
        </SimpleGrid>
        <Stack gap={4}>
          <Switch
            label={t("assignOwnEmploymentNumber")}
            {...form.getInputProps("assignOwnNumber", { type: "checkbox" })}
          />
          {form.values.assignOwnNumber ? (
            <TextInput
              label={t("employmentNumber")}
              description={t("employmentNumberDescription")}
              withAsterisk
              {...form.getInputProps("employmentNumber")}
            />
          ) : (
            <Text size="sm" c="dimmed">
              {t("employmentNumberGenerated", { number: `${person.employeeNumber}-1` })}
            </Text>
          )}
        </Stack>
        <TermsFields
          form={form}
          structure={structure}
          canSeePay={meta?.capabilities.canSeePay ?? false}
          moreOpen={moreOpen}
          onToggleMore={() => setMoreOpen((open) => !open)}
          hoursTouched={hoursTouched}
          onHoursTouched={() => setHoursTouched(true)}
        />
        {moreOpen && (
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <Select
              label={t("relationshipType")}
              description={t("relationshipTypeDescription")}
              clearable
              data={relationshipTypes.map((value) => ({ value, label: t(`relationshipType_${value}`) }))}
              {...form.getInputProps("relationshipType")}
            />
            <DateInput
              label={t("contractSignedAt")}
              valueFormat={t("dateInputFormat")}
              clearable
              {...form.getInputProps("contractSignedAt")}
            />
          </SimpleGrid>
        )}
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("addLater")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("saveEmployment")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
