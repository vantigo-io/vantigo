import { ActionIcon, Alert, Anchor, Button, Card, Divider, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAddressBook, IconMail, IconPencil, IconPhone, IconWorld } from "@tabler/icons-react";
import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import type { ComponentType, ReactNode } from "react";
import { useState } from "react";
import {
  ApiConflictError,
  ApiValidationError,
  type CustomerContactInfo,
  type CustomerResponse,
  customerQueryOptions,
  syncCustomerRevision,
  updateContactInfo,
} from "../api/customers";
import { useCustomerReload } from "../lib/customer-reload";
import { CustomerAddressesSection } from "./-customer-address-list";
import "../i18n";

/**
 * The customer page's "Contact & addresses" card (design D6): the
 * customer's own email/phone/website with an edit modal, and its typed
 * addresses below. `canEdit` comes from the host, which reads the caller's
 * `customers:update` permission — this package never fetches permissions
 * itself (same convention as `canArchive`/`canRestore` on the page header).
 * Contact info rides on the customer row (design D2), already fetched by
 * `CustomerDetailHeader` under the same query key, so this card reads it
 * off that cache rather than issuing a query of its own; addresses are a
 * sub-resource (design D1) with their own query.
 */
export const CustomerContactCard = ({ customerId, canEdit }: { customerId: number; canEdit?: boolean }) => {
  const { t } = useI18n("customers");
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const [contactModalOpened, setContactModalOpened] = useState(false);

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="lg">
        <Group gap="xs">
          <IconAddressBook size={18} stroke={1.5} />
          {/* A card title is a heading, as the timeline's own is — the page
              otherwise reads as one flat block to assistive tech. */}
          <Text fw={600} component="h3">
            {t("contactAndAddresses")}
          </Text>
        </Group>

        <ContactInfoSection
          contactInfo={customer.contactInfo}
          canEdit={canEdit}
          onEdit={() => setContactModalOpened(true)}
        />

        <Divider />

        <CustomerAddressesSection customerId={customerId} canEdit={canEdit} />
      </Stack>

      <CustomerContactInfoModal
        opened={contactModalOpened}
        customer={customer}
        onClose={() => setContactModalOpened(false)}
      />
    </Card>
  );
};

const ContactInfoRow = ({
  label,
  icon: Icon,
  value,
  render,
}: {
  label: string;
  icon: ComponentType<{ size?: number; color?: string }>;
  value: string | null;
  render: (value: string) => ReactNode;
}) => (
  <Group gap="xs" wrap="nowrap">
    <Icon size={14} color="var(--mantine-color-gray-6)" />
    <Text size="sm" c="dimmed" miw={64}>
      {label}
    </Text>
    {value ? render(value) : <Text size="sm">—</Text>}
  </Group>
);

const ContactInfoSection = ({
  contactInfo,
  canEdit,
  onEdit,
}: {
  contactInfo: CustomerContactInfo | null | undefined;
  canEdit?: boolean;
  onEdit: () => void;
}) => {
  const { t } = useI18n("customers");
  const email = contactInfo?.email ?? null;
  const phone = contactInfo?.phone ?? null;
  const website = contactInfo?.website ?? null;
  const hasAny = email !== null || phone !== null || website !== null;

  return (
    <Stack gap="xs">
      {hasAny ? (
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <Stack gap={6}>
            <ContactInfoRow
              label={t("email")}
              icon={IconMail}
              value={email}
              render={(value) => (
                <Anchor href={`mailto:${value}`} size="sm">
                  {value}
                </Anchor>
              )}
            />
            <ContactInfoRow
              label={t("phone")}
              icon={IconPhone}
              value={phone}
              render={(value) => (
                <Anchor href={`tel:${value.replace(/\s+/g, "")}`} size="sm">
                  {value}
                </Anchor>
              )}
            />
            <ContactInfoRow
              label={t("website")}
              icon={IconWorld}
              value={website}
              render={(value) => (
                <Anchor href={value} target="_blank" rel="noopener noreferrer" size="sm">
                  {value}
                </Anchor>
              )}
            />
          </Stack>
          {canEdit && (
            <ActionIcon variant="subtle" color="gray" aria-label={t("editContactDetails")} onClick={onEdit}>
              <IconPencil size={16} />
            </ActionIcon>
          )}
        </Group>
      ) : (
        <EmptyState
          size="sm"
          title={t("noContactDetailsYet")}
          action={
            canEdit ? (
              <Button variant="light" size="xs" leftSection={<IconPencil size={14} />} onClick={onEdit}>
                {t("editContactDetails")}
              </Button>
            ) : undefined
          }
        />
      )}
    </Stack>
  );
};

interface ContactInfoFormValues {
  email: string;
  phone: string;
  website: string;
}

const contactInfoValues = (customer: CustomerResponse): ContactInfoFormValues => ({
  email: customer.contactInfo?.email ?? "",
  phone: customer.contactInfo?.phone ?? "",
  website: customer.contactInfo?.website ?? "",
});

/**
 * Edits a customer's contact info (design D2). Unlike `CustomerFormModal`
 * there is no create/edit distinction — contact info always exists
 * conceptually (every field simply nullable) — so `opened` alone drives the
 * modal, and the open-transition re-seeds the form and the modal-local
 * revision the same way `CustomerFormModal`'s `state` change does.
 */
const CustomerContactInfoModal = ({
  opened,
  customer,
  onClose,
}: {
  opened: boolean;
  customer: CustomerResponse;
  onClose: () => void;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const [conflict, setConflict] = useState(false);
  const [revision, setRevision] = useState(customer.revision);
  // Re-seeds the form and the revision this modal will send next whenever it
  // is (re)opened — adjusted during render, the same pattern
  // `CustomerFormModal` uses for its own `state !== seenState` check. It is
  // deliberately keyed on the open transition, not on `customer` itself: a
  // background refetch of the customer while the modal is open (say, from
  // another tab's edit) must not blow away what the caller is mid-typing.
  const [wasOpened, setWasOpened] = useState(opened);
  const form = useForm<ContactInfoFormValues>({ initialValues: contactInfoValues(customer) });
  // The conflict alert's Reload — one fetch, and a failure that says so
  // rather than quietly re-seeding the revision the server already refused
  // (see `useCustomerReload`).
  const reload = useCustomerReload({
    customerId: customer.id,
    queryKey: customerQueryOptions(customer.id).queryKey,
    fetchFresh: () => queryClient.fetchQuery({ ...customerQueryOptions(customer.id), staleTime: 0 }),
    revisionOf: (fresh) => fresh.revision,
    seed: (fresh) => {
      form.setValues(contactInfoValues(fresh));
      form.resetDirty();
      form.clearErrors();
      setRevision(fresh.revision);
      setConflict(false);
    },
  });
  if (opened !== wasOpened) {
    setWasOpened(opened);
    if (opened) {
      form.setValues(contactInfoValues(customer));
      form.resetDirty();
      form.clearErrors();
      setRevision(customer.revision);
      setConflict(false);
      reload.forget();
    }
  }

  const mutation = useMutation({
    mutationFn: (values: ContactInfoFormValues) =>
      updateContactInfo(customer.id, {
        email: values.email.trim() || null,
        phone: values.phone.trim() || null,
        website: values.website.trim() || null,
        revision,
      }),
    onSuccess: (saved) => {
      // The 200 body is the whole customer, so the row's fresh revision is
      // in hand: write it to every cache entry that carries it before the
      // invalidation's refetches have had time to land, or the next editor
      // opened in that window sends the revision this save just replaced.
      syncCustomerRevision(queryClient, customer.id, saved.revision);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      setConflict(false);
      onClose();
      notifications.show({ color: "teal", title: t("contactInfoUpdated"), message: t("contactInfoSaved") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      if (error instanceof ApiConflictError && !error.code) {
        // Design D5's revision conflict, same as CustomerFormModal's own:
        // nothing to fix but look at the latest version.
        setConflict(true);
        return;
      }
      notifications.show({ color: "red", title: t("contactInfoCouldNotBeSaved"), message: error.message });
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("editContactDetails")} centered>
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          {conflict && (
            <Alert color="yellow" title={t("customerChangedTitle")}>
              <Stack gap="xs">
                <Text size="sm">{t("customerChangedMessage")}</Text>
                <Text size="sm">{t("customerChangesNotSaved")}</Text>
                {reload.failed && (
                  <Text size="sm" c="red">
                    {t("couldNotReload")}
                  </Text>
                )}
                <Group justify="flex-end">
                  <Button size="xs" variant="light" color="yellow" loading={reload.reloading} onClick={reload.reload}>
                    {t("reload")}
                  </Button>
                </Group>
              </Stack>
            </Alert>
          )}
          <TextInput
            label={t("email")}
            placeholder={t("emailPlaceholder")}
            data-autofocus
            {...form.getInputProps("email")}
          />
          <TextInput label={t("phone")} placeholder={t("phonePlaceholder")} {...form.getInputProps("phone")} />
          <TextInput label={t("website")} placeholder="https://…" {...form.getInputProps("website")} />
          <Group justify="flex-end">
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
