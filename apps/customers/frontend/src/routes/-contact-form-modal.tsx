import { Button, Chip, Group, Modal, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { type ContactInput, type ContactResponse, createContact, updateContact } from "../api/contacts";
import { ApiValidationError } from "../api/customers";
import { formatContactName } from "../lib/format-contact-name";

export type ContactModalState = { mode: "create" } | { mode: "edit"; contact: ContactResponse };

interface ContactFormValues {
  firstName: string;
  lastName: string;
  middleName: string;
  prefix: string;
  suffix: string;
  phone: string;
  email: string;
}

const emptyValues: ContactFormValues = {
  firstName: "",
  lastName: "",
  middleName: "",
  prefix: "",
  suffix: "",
  phone: "",
  email: "",
};

const valuesFromState = (state: ContactModalState): ContactFormValues =>
  state.mode === "create"
    ? emptyValues
    : {
        firstName: state.contact.firstName,
        lastName: state.contact.lastName,
        middleName: state.contact.middleName ?? "",
        prefix: state.contact.prefix ?? "",
        suffix: state.contact.suffix ?? "",
        phone: state.contact.phone ?? "",
        email: state.contact.email ?? "",
      };

/** Maps form values to the API input, dropping blank optional fields. */
export const toContactInput = (values: ContactFormValues): ContactInput => ({
  firstName: values.firstName.trim(),
  lastName: values.lastName.trim(),
  middleName: values.middleName.trim() || undefined,
  prefix: values.prefix.trim() || undefined,
  suffix: values.suffix.trim() || undefined,
  phone: values.phone.trim() || undefined,
  email: values.email.trim() || undefined,
});

interface ContactFormModalProps {
  state: ContactModalState | null;
  onClose: () => void;
}

/**
 * Modal for creating a new contact or editing an existing one. Only the first and
 * last name are required — everything else can be filled in as it becomes known.
 */
export const ContactFormModal = ({ state, onClose }: ContactFormModalProps) => {
  const queryClient = useQueryClient();
  const isEdit = state?.mode === "edit";

  const form = useForm<ContactFormValues>({
    initialValues: emptyValues,
    validate: {
      firstName: (value) => (value.trim().length === 0 ? "First name is required" : null),
      lastName: (value) => (value.trim().length === 0 ? "Last name is required" : null),
    },
  });

  // Sync form values when the modal opens for a different contact (or create).
  // The form object is recreated each render but its methods are stable, so it is
  // intentionally excluded from the dependency array.
  useEffect(() => {
    if (state) {
      form.setValues(valuesFromState(state));
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  const mutation = useMutation({
    mutationFn: (input: ContactInput) => (isEdit ? updateContact(state.contact.id, input) : createContact(input)),
    onSuccess: (contact) => {
      notifications.show({
        color: "teal",
        title: isEdit ? "Contact updated" : "Contact created",
        message: `"${formatContactName(contact)}" was ${isEdit ? "updated" : "created"} successfully.`,
      });
      queryClient.invalidateQueries({ queryKey: ["contacts"] });
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({
        color: "red",
        title: isEdit ? "Failed to update contact" : "Failed to create contact",
        message: error.message,
      });
    },
  });

  const handleSubmit = form.onSubmit((values) => {
    mutation.mutate(toContactInput(values));
  });

  return (
    <Modal opened={state !== null} onClose={onClose} title={isEdit ? "Edit contact" : "Create new contact"} centered>
      <form onSubmit={handleSubmit}>
        <Stack>
          <ContactFields
            key={isEdit ? state.contact.id : "create"}
            getInputProps={form.getInputProps}
            setFieldValue={form.setFieldValue}
            values={form.values}
          />
          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? "Save changes" : "Create contact"}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};

/** The optional name parts hidden behind chips, in their natural display order. */
const optionalNameParts = [
  { field: "prefix", label: "Prefix", placeholder: "e.g. Dr." },
  { field: "middleName", label: "Middle name", placeholder: "e.g. Bernhard" },
  { field: "suffix", label: "Suffix", placeholder: "e.g. PhD" },
] as const;

type OptionalNamePart = (typeof optionalNameParts)[number]["field"];

interface ContactFieldsProps {
  getInputProps: (path: string) => object;
  setFieldValue: (path: string, value: string) => void;
  /** The current values of the optional name parts, used to pre-open their chips. */
  values: Record<OptionalNamePart, string>;
}

/**
 * The contact's own fields, shared between this modal and the add-contact flow on
 * the customer dashboard. Only first and last name are always visible — the
 * optional name parts hide behind toggle chips and are cleared when toggled off,
 * so hidden values are never submitted.
 */
export const ContactFields = ({ getInputProps, setFieldValue, values }: ContactFieldsProps) => {
  const [toggledParts, setToggledParts] = useState<string[]>([]);

  // Visibility is derived: a part is shown when it holds a value (e.g. prefilled
  // while editing) or when its chip was toggled on. Toggling a chip off clears the
  // value, so hidden values are never submitted.
  const visibleParts = optionalNameParts
    .map((part) => part.field)
    .filter((field) => values[field] || toggledParts.includes(field));

  const toggleParts = (parts: string[]) => {
    for (const part of optionalNameParts) {
      if (!parts.includes(part.field)) {
        setFieldValue(part.field, "");
      }
    }
    setToggledParts(parts);
  };

  const shownParts = optionalNameParts.filter((part) => visibleParts.includes(part.field));

  return (
    <>
      <Group grow>
        <TextInput
          label="First name"
          placeholder="e.g. Anders"
          withAsterisk
          data-autofocus
          {...getInputProps("firstName")}
        />
        <TextInput label="Last name" placeholder="e.g. Refsdal" withAsterisk {...getInputProps("lastName")} />
      </Group>

      <Chip.Group multiple value={visibleParts} onChange={toggleParts}>
        <Group gap="xs">
          {optionalNameParts.map((part) => (
            <Chip key={part.field} value={part.field} size="xs" variant="light">
              + {part.label}
            </Chip>
          ))}
        </Group>
      </Chip.Group>

      {shownParts.length > 0 && (
        <Group grow>
          {shownParts.map((part) => (
            <TextInput
              key={part.field}
              label={part.label}
              placeholder={part.placeholder}
              {...getInputProps(part.field)}
            />
          ))}
        </Group>
      )}

      <Group grow>
        <TextInput label="Phone" placeholder="e.g. +47 934 89 731" {...getInputProps("phone")} />
        <TextInput label="Email" placeholder="e.g. anders@refsdal.no" {...getInputProps("email")} />
      </Group>
    </>
  );
};
