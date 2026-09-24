import { Button, Checkbox, Group, Input, Modal, Stack, Switch, Text, TextInput, Tooltip } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { useEffect } from "react";
import {
  CONTACT_ROLES,
  type ContactRoleAssignment,
  type ContactRoleInput,
  updateCustomerContact,
} from "../api/contacts";
import { ApiValidationError } from "../api/customers";
import { NoValue } from "../components/legal-badges";
import { contactRoleLabel, primaryContactLabel } from "../lib/contact-role-label";
import { customerWriteErrorMessage } from "../lib/customer-write-error";
import "../i18n";

/**
 * Shows the connection-specific value when set, otherwise falls back to the
 * contact's own value (dimmed, to signal it is inherited).
 */
export const ConnectionValue = ({ own, connection }: { own: string | null; connection: string | null }) => {
  const { t } = useI18n("customers");
  if (connection) {
    return <Text size="sm">{connection}</Text>;
  }

  if (own) {
    return (
      <Tooltip label={t("inheritedValueTooltip")}>
        <Text size="sm" c="dimmed">
          {own}
        </Text>
      </Tooltip>
    );
  }

  return <NoValue />;
};

/**
 * The association's editable state, as the form holds it (design D5). The two
 * `Record`s are keyed by role code: `roles` is which checkboxes are ticked,
 * `primary` which of the ticked ones asked to be primary. Two flat maps rather
 * than an array of objects, because that is what a checkbox group and a switch
 * bind to without a reducer in between.
 */
export interface ConnectionFormValues {
  title: string;
  roles: Record<string, boolean>;
  primary: Record<string, boolean>;
  phone: string;
  email: string;
}

/** The empty form: no title, nothing ticked. */
// eslint-disable-next-line react-refresh/only-export-components
export const emptyConnectionValues = (): ConnectionFormValues => ({
  title: "",
  roles: {},
  primary: {},
  phone: "",
  email: "",
});

/**
 * The form's roles as the API's `roles` array (design D3: the complete set to
 * hold), in the fixed order so the request looks the same whatever order the
 * boxes were ticked in.
 *
 * `primary` is sent only when the switch is ON, and OMITTED otherwise — the API's
 * three-valued flag, used as it is meant to be. An omitted flag says "leave this
 * role's primary as it is, or let the first-holder rule decide if it is new",
 * which is exactly what an untouched switch means; sending an explicit `false`
 * instead would be the server's one refusal (`primary: false` on the primary
 * holder), so a UI that echoed every switch would turn ticking a second role into
 * a 400. There is deliberately no way to demote from here: the server refuses it,
 * and the way to move a primary is to make another contact primary instead.
 *
 * `values.roles` can hold a key outside `CONTACT_ROLES`: the vocabulary is a
 * value change on the server rather than a migration (design D2), so a contact
 * can already hold a role this frontend's catalog does not know, seeded into
 * the form from the association's own roles. This is a complete-set replace
 * (design D3), so silently iterating only `CONTACT_ROLES` would delete that
 * role server-side the next time anything here is saved. It is included with
 * `primary` always omitted — there is no checkbox or switch for it, so
 * "unchanged" is the only honest thing the UI can say about it.
 */
// eslint-disable-next-line react-refresh/only-export-components
export const toRoleInputs = (values: ConnectionFormValues): ContactRoleInput[] => {
  const knownRoles = CONTACT_ROLES as readonly string[];
  const unknownHeldRoles = Object.keys(values.roles).filter((role) => !knownRoles.includes(role));
  return [...CONTACT_ROLES, ...unknownHeldRoles]
    .filter((role) => values.roles[role])
    .map((role) => (knownRoles.includes(role) && values.primary[role] ? { role, primary: true } : { role }));
};

/**
 * The title-or-role rule the server enforces (design D1), checked here too so
 * the user is told before a round trip rather than after one. The message is
 * shown on the title, the field the server keys its own refusal to.
 */
// eslint-disable-next-line react-refresh/only-export-components
export const validateConnectionValues = (t: (key: string) => string) => ({
  title: (value: string, values: ConnectionFormValues) =>
    value.trim().length === 0 && toRoleInputs(values).length === 0 ? t("titleOrRoleRequired") : null,
});

interface ConnectionFieldsProps {
  getInputProps: (path: string) => object;
  values: ConnectionFormValues;
  setFieldValue: (path: string, value: unknown) => void;
  /**
   * Clears one field's validation error — used to drop a stale
   * `titleOrRoleRequired` off the title the moment a role tick answers it,
   * rather than leaving it to sit until the next submit re-validates.
   */
  clearFieldError: (path: string) => void;
  /**
   * Roles this association currently holds AS primary, per the last read from
   * the server: clearing one is refused (design D2), so its switch is on and
   * disabled rather than offering a change that cannot happen.
   */
  lockedPrimary: string[];
  /**
   * Of those, the ones no other contact holds at all. The two cases get
   * different reasons because only one of them is true, and the contact page
   * cannot know which — it passes an empty array and gets the general wording.
   */
  soleRoles: string[];
}

export const ConnectionFields = ({
  getInputProps,
  values,
  setFieldValue,
  clearFieldError,
  lockedPrimary,
  soleRoles,
}: ConnectionFieldsProps) => {
  const { t } = useI18n("customers");
  return (
    <>
      <TextInput label={t("contactTitle")} placeholder={t("contactTitlePlaceholder")} {...getInputProps("title")} />
      <Input.Wrapper
        label={t("contactRoles")}
        description={t("contactRolesDescription")}
        error={getRolesError(getInputProps)}
      >
        <Stack gap="xs" mt="xs">
          {CONTACT_ROLES.map((role) => {
            const checked = values.roles[role] ?? false;
            // Locked only while the role is actually ticked: unticking it is
            // what asks to drop the role altogether, and a switch that stayed
            // ON with "Already the only holder" showing would say the primary
            // request survives a role that is no longer held — it does not.
            const locked = checked && lockedPrimary.includes(role);
            const reason = soleRoles.includes(role) ? t("roleOnlyHolder") : t("rolePrimaryStays");
            return (
              <Group key={role} justify="space-between" wrap="nowrap">
                <Checkbox
                  label={contactRoleLabel(t, role)}
                  checked={checked}
                  onChange={(event) => {
                    const next = event.currentTarget.checked;
                    setFieldValue(`roles.${role}`, next);
                    // Unticking a role also drops its primary request, so a
                    // re-tick does not silently carry the old one back.
                    if (!next) setFieldValue(`primary.${role}`, false);
                    // Ticking a role can satisfy the title-or-role rule on its
                    // own, so a stale "give a title or pick a role" error must
                    // not survive the tick that just answered it.
                    if (next) clearFieldError("title");
                    // Either direction changes the role set the server just
                    // refused, so a stale server-side `roles` error must not
                    // survive a tick or untick that may already have answered it.
                    clearFieldError("roles");
                  }}
                />
                <Tooltip label={reason} disabled={!locked}>
                  <Switch
                    label={t("primaryBadge")}
                    aria-label={primaryContactLabel(t, role)}
                    labelPosition="left"
                    size="sm"
                    checked={locked || (values.primary[role] ?? false)}
                    disabled={!checked || locked}
                    description={locked ? reason : undefined}
                    onChange={(event) => setFieldValue(`primary.${role}`, event.currentTarget.checked)}
                  />
                </Tooltip>
              </Group>
            );
          })}
        </Stack>
      </Input.Wrapper>
      <Group grow>
        <TextInput
          label={t("connectionPhoneLabel")}
          description={t("connectionPhoneDescription")}
          {...getInputProps("phone")}
        />
        <TextInput
          label={t("connectionEmailLabel")}
          description={t("connectionEmailDescription")}
          {...getInputProps("email")}
        />
      </Group>
    </>
  );
};

/**
 * The server keys its role refusals to `roles`, which is a group here and not
 * an input, so its error is read off the form and shown on the wrapper — the
 * one place a `roles` message can land where a user will see it.
 */
const getRolesError = (getInputProps: (path: string) => object) =>
  (getInputProps("roles") as { error?: ReactNode }).error;

/** Identifies the association being edited plus its current values and modal title. */
export interface EditConnectionTarget {
  customerId: number;
  contactId: number;
  /** The name of the counterpart shown in the modal title. */
  counterpartName: string;
  title: string | null;
  roles: ContactRoleAssignment[];
  /** Roles this contact is the ONLY holder of, for the disabled switch's reason. */
  soleRoles: string[];
  phone: string | null;
  email: string | null;
}

interface EditConnectionModalProps {
  target: EditConnectionTarget | null;
  onClose: () => void;
}

/**
 * Edits the title, roles and connection-specific contact details of a
 * customer-contact association. Shared between the customer dashboard (editing
 * a contact's connection) and the contact dashboard (editing a customer's
 * connection).
 */
export const EditConnectionModal = ({ target, onClose }: EditConnectionModalProps) => {
  const queryClient = useQueryClient();
  const { t } = useI18n("customers");

  const form = useForm<ConnectionFormValues>({
    initialValues: emptyConnectionValues(),
    validate: validateConnectionValues(t),
  });

  const lockedPrimary = (target?.roles ?? []).filter((r) => r.primary).map((r) => r.role);

  // Sync form values when the modal opens for a different association.
  // The form object is recreated each render but its methods are stable, so it is
  // intentionally excluded from the dependency array.
  useEffect(() => {
    if (target) {
      form.setValues({
        title: target.title ?? "",
        roles: Object.fromEntries(target.roles.map((r) => [r.role, true])),
        primary: Object.fromEntries(target.roles.map((r) => [r.role, r.primary])),
        phone: target.phone ?? "",
        email: target.email ?? "",
      });
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);

  const mutation = useMutation({
    mutationFn: (values: ConnectionFormValues) => {
      if (!target) {
        throw new Error(t("editConnection"));
      }

      return updateCustomerContact(target.customerId, target.contactId, {
        title: values.title.trim() || undefined,
        roles: toRoleInputs(values),
        phone: values.phone.trim() || undefined,
        email: values.email.trim() || undefined,
      });
    },
    onSuccess: () => {
      notifications.show({
        color: "teal",
        title: t("connectionUpdated"),
        message: target ? t("connectionUpdatedMessage", { name: target.counterpartName }) : "",
      });
      if (target) {
        queryClient.invalidateQueries({ queryKey: ["customers", target.customerId, "contacts"] });
        queryClient.invalidateQueries({ queryKey: ["contacts", target.contactId, "customers"] });
        queryClient.invalidateQueries({ queryKey: ["customers", target.customerId, "timeline"] });
      }
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({
        color: "red",
        title: t("failedUpdateConnection"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });

  return (
    <Modal
      opened={target !== null}
      onClose={onClose}
      title={target ? t("editConnection", { name: target.counterpartName }) : ""}
      centered
    >
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          <ConnectionFields
            getInputProps={form.getInputProps}
            values={form.values}
            setFieldValue={form.setFieldValue}
            clearFieldError={form.clearFieldError}
            lockedPrimary={lockedPrimary}
            soleRoles={target?.soleRoles ?? []}
          />
          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {t("saveChanges")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
