"use client";

import { Button, Group, Stack, Textarea, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import type { Contact } from "@vantigo/customers/database/schema/contacts";
import { useTranslations } from "next-intl";
import type { ContactCreateInput } from "./schemas";

export interface ContactFormValues {
  name: string;
  email: string;
  phone: string;
  notes: string;
}

export function toContactInput(values: ContactFormValues): ContactCreateInput {
  return {
    name: values.name.trim(),
    email: values.email.trim() || null,
    phone: values.phone.trim() || null,
    notes: values.notes.trim() || null,
  };
}

export function useContactForm(initialValues?: Contact | null) {
  const t = useTranslations("contacts.form");
  return useForm<ContactFormValues>({
    mode: "uncontrolled",
    initialValues: {
      name: initialValues?.name ?? "",
      email: initialValues?.email ?? "",
      phone: initialValues?.phone ?? "",
      notes: initialValues?.notes ?? "",
    },
    validate: {
      name: (value) => (value.trim() ? null : t("nameRequired")),
      email: (value) => (!value.trim() || /^\S+@\S+\.\S+$/.test(value) ? null : t("invalidEmail")),
    },
  });
}

/** Person fields shared by all create/edit contact forms. */
export function ContactFields({ form }: Readonly<{ form: ReturnType<typeof useContactForm> }>) {
  const t = useTranslations("contacts.form");

  return (
    <>
      <TextInput
        label={t("name")}
        placeholder={t("namePlaceholder")}
        withAsterisk
        key={form.key("name")}
        {...form.getInputProps("name")}
      />
      <Group grow>
        <TextInput
          label={t("email")}
          placeholder={t("emailPlaceholder")}
          key={form.key("email")}
          {...form.getInputProps("email")}
        />
        <TextInput
          label={t("phone")}
          placeholder={t("phonePlaceholder")}
          key={form.key("phone")}
          {...form.getInputProps("phone")}
        />
      </Group>
      <Textarea
        label={t("notes")}
        autosize
        minRows={2}
        maxRows={4}
        key={form.key("notes")}
        {...form.getInputProps("notes")}
      />
    </>
  );
}

/** Complete standalone create/edit form with submit button. */
export function ContactForm({
  initialValues,
  isSubmitting,
  submitLabel,
  onSubmit,
}: Readonly<{
  initialValues?: Contact | null;
  isSubmitting: boolean;
  submitLabel: string;
  onSubmit: (input: ContactCreateInput) => void;
}>) {
  const form = useContactForm(initialValues);

  return (
    <form onSubmit={form.onSubmit((values) => onSubmit(toContactInput(values)))}>
      <Stack gap="sm">
        <ContactFields form={form} />
        <Group justify="flex-end" mt="xs">
          <Button type="submit" loading={isSubmitting}>
            {submitLabel}
          </Button>
        </Group>
      </Stack>
    </form>
  );
}
