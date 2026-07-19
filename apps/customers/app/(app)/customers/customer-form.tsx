"use client";

import {
  Alert,
  Anchor,
  Button,
  Group,
  Input,
  SegmentedControl,
  Select,
  Stack,
  Textarea,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { IconAlertTriangle } from "@tabler/icons-react";
import type { Customer } from "@vantigo/customers/database/schema/customers";
import type { DuplicateWarning } from "@vantigo/customers/lib/customers/queries";
import {
  type CustomerCreateInput,
  customerCreateSchema,
} from "@vantigo/customers/lib/customers/schemas";
import { zod4Resolver } from "mantine-form-zod-resolver";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { BrregSearch } from "./brreg-search";

export interface CustomerFormProps {
  initialValues?: Customer;
  isSubmitting: boolean;
  /** Duplicate warnings returned by the API after a save. */
  warnings?: DuplicateWarning[];
  submitLabel: string;
  onSubmit: (values: CustomerCreateInput) => void;
}

const COUNTRY_OPTIONS = ["NO", "SE", "DK", "FI", "DE", "GB", "US"];

export function CustomerForm({
  initialValues,
  isSubmitting,
  warnings,
  submitLabel,
  onSubmit,
}: CustomerFormProps) {
  const t = useTranslations("customers.form");

  const form = useForm<CustomerCreateInput>({
    mode: "uncontrolled",
    initialValues: {
      legalName: initialValues?.legalName ?? "",
      legalType: initialValues?.legalType ?? "business",
      legalCountry: initialValues?.legalCountry ?? "NO",
      legalId: initialValues?.legalId ?? "",
      email: initialValues?.email ?? "",
      phone: initialValues?.phone ?? "",
      notes: initialValues?.notes ?? "",
    },
    validate: (values) => {
      const result = zod4Resolver(customerCreateSchema)(values);
      // Map the machine-readable zod message to a translated one.
      if (result.legalId === "invalid_legal_id") {
        result.legalId = t("invalidLegalId", {
          legalType: values.legalType,
          country: values.legalCountry,
        });
      }
      return result;
    },
  });

  // The country/type pair decides validation rules, the legal-id label and
  // whether the brreg search is available — track them reactively.
  const [legalCountry, setLegalCountry] = useState(form.getValues().legalCountry);
  const [legalType, setLegalType] = useState(form.getValues().legalType);
  form.watch("legalCountry", ({ value }) => setLegalCountry(value));
  form.watch("legalType", ({ value }) => setLegalType(value));

  const isNorwegian = legalCountry === "NO";
  const showBrregSearch = isNorwegian && legalType === "business";
  const legalIdLabel = isNorwegian
    ? legalType === "business"
      ? t("legalIdOrgnr")
      : t("legalIdFodselsnummer")
    : t("legalId");

  return (
    <form
      onSubmit={form.onSubmit((values) =>
        onSubmit({ ...values, notes: values.notes?.trim() ? values.notes : null }),
      )}
    >
      <Stack gap="sm">
        {warnings && warnings.length > 0 && (
          <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("duplicateTitle")}>
            {warnings.map((warning) => (
              <div key={warning.code}>
                {t(warning.code === "DUPLICATE_LEGAL_ID" ? "duplicateLegalId" : "duplicateEmail")}{" "}
                <Anchor component={Link} href={`/customers/${warning.existingId}`} size="sm">
                  #{warning.existingId}
                </Anchor>
              </div>
            ))}
          </Alert>
        )}

        <Group grow align="flex-end">
          <Select
            label={t("legalCountry")}
            data={COUNTRY_OPTIONS}
            searchable
            allowDeselect={false}
            withAsterisk
            data-autofocus
            key={form.key("legalCountry")}
            {...form.getInputProps("legalCountry")}
          />
          <Input.Wrapper label={t("legalType")} withAsterisk>
            <SegmentedControl
              fullWidth
              data={[
                { value: "business", label: t("legalTypeBusiness") },
                { value: "private", label: t("legalTypePrivate") },
              ]}
              key={form.key("legalType")}
              {...form.getInputProps("legalType")}
            />
          </Input.Wrapper>
        </Group>

        {showBrregSearch && (
          <BrregSearch
            onSelect={(company) => {
              // An explicit selection overwrites; manual edits afterwards
              // are never touched (fills only happen right here). Contact
              // fields are voluntarily registered at brreg and only
              // overwrite when actually present.
              form.setFieldValue("legalId", company.orgnr);
              form.setFieldValue("legalName", company.name);
              if (company.email) form.setFieldValue("email", company.email);
              if (company.phone) form.setFieldValue("phone", company.phone);
            }}
          />
        )}

        <TextInput
          label={legalIdLabel}
          placeholder={isNorwegian ? legalIdLabel : t("legalIdPlaceholder")}
          withAsterisk
          key={form.key("legalId")}
          {...form.getInputProps("legalId")}
        />

        <TextInput
          label={t("legalName")}
          placeholder={t("legalNamePlaceholder")}
          withAsterisk
          key={form.key("legalName")}
          {...form.getInputProps("legalName")}
        />

        <Group grow>
          <TextInput
            label={t("email")}
            placeholder={t("emailPlaceholder")}
            withAsterisk
            key={form.key("email")}
            {...form.getInputProps("email")}
          />
          <TextInput
            label={t("phone")}
            placeholder={t("phonePlaceholder")}
            withAsterisk
            key={form.key("phone")}
            {...form.getInputProps("phone")}
          />
        </Group>

        <Textarea
          label={t("notes")}
          autosize
          minRows={2}
          maxRows={6}
          key={form.key("notes")}
          {...form.getInputProps("notes")}
        />

        <Group justify="flex-end" mt="xs">
          <Button type="submit" loading={isSubmitting}>
            {submitLabel}
          </Button>
        </Group>
      </Stack>
    </form>
  );
}
