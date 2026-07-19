"use client";

import {
  ActionIcon,
  Anchor,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  Loader,
  Menu,
  Modal,
  SegmentedControl,
  Select,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useDebouncedValue, useDisclosure } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconDots, IconLinkOff, IconPencil, IconPlus, IconStar } from "@tabler/icons-react";
import {
  ContactFields,
  toContactInput,
  useContactForm,
} from "@vantigo/customers/lib/contacts/contact-form";
import {
  useAddCustomerContact,
  useContacts,
  useCustomerContacts,
  useUnlinkCustomerContact,
  useUpdateCustomerContact,
} from "@vantigo/customers/lib/contacts/hooks";
import type { CustomerContactItem } from "@vantigo/customers/lib/contacts/queries";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { useState } from "react";

export function ContactsCard({ customerId }: Readonly<{ customerId: number }>) {
  const t = useTranslations("contacts.card");
  const { data: contacts, isLoading } = useCustomerContacts(customerId);
  const updateLink = useUpdateCustomerContact(customerId);
  const unlink = useUnlinkCustomerContact(customerId);

  const [addOpened, add] = useDisclosure(false);
  const [editingLink, setEditingLink] = useState<CustomerContactItem | null>(null);

  const notifyError = (error: Error) =>
    notifications.show({ color: "red", message: error.message });

  const setPrimary = (contact: CustomerContactItem) => {
    updateLink.mutate(
      { contactId: contact.id, input: { isPrimary: true } },
      { onError: notifyError },
    );
  };

  const confirmUnlink = (contact: CustomerContactItem) => {
    modals.openConfirmModal({
      title: t("unlinkTitle"),
      children: <Text size="sm">{t("unlinkMessage", { name: contact.name })}</Text>,
      labels: { confirm: t("unlinkConfirm"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () =>
        unlink.mutate(contact.id, {
          onSuccess: () =>
            notifications.show({ color: "green", message: t("unlinked", { name: contact.name }) }),
          onError: notifyError,
        }),
    });
  };

  return (
    <Card withBorder>
      <Group justify="space-between" mb="sm">
        <Title order={4}>{t("title")}</Title>
        <Button size="xs" leftSection={<IconPlus size={14} />} onClick={add.open}>
          {t("add")}
        </Button>
      </Group>

      {isLoading ? (
        <Group justify="center" py="md">
          <Loader size="sm" />
        </Group>
      ) : !contacts || contacts.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("empty")}
        </Text>
      ) : (
        <Stack gap="sm">
          {contacts.map((contact) => (
            <Group key={contact.id} wrap="nowrap" justify="space-between">
              <div>
                <Group gap="xs">
                  <Anchor component={Link} href={`/contacts/${contact.id}`} size="sm" fw={500}>
                    {contact.name}
                  </Anchor>
                  {contact.isPrimary && (
                    <Badge size="xs" color="teal" variant="light">
                      {t("primary")}
                    </Badge>
                  )}
                  {contact.role && (
                    <Text size="xs" c="dimmed">
                      {contact.role}
                    </Text>
                  )}
                </Group>
                <Text size="xs" c="dimmed">
                  {[contact.email, contact.phone].filter(Boolean).join(" · ") || "–"}
                </Text>
              </div>

              <Menu withinPortal position="bottom-end">
                <Menu.Target>
                  <ActionIcon variant="subtle" color="gray" aria-label={t("actions")}>
                    <IconDots size={16} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  {!contact.isPrimary && (
                    <Menu.Item
                      leftSection={<IconStar size={14} />}
                      onClick={() => setPrimary(contact)}
                    >
                      {t("makePrimary")}
                    </Menu.Item>
                  )}
                  <Menu.Item
                    leftSection={<IconPencil size={14} />}
                    onClick={() => setEditingLink(contact)}
                  >
                    {t("editRole")}
                  </Menu.Item>
                  <Menu.Item
                    color="red"
                    leftSection={<IconLinkOff size={14} />}
                    onClick={() => confirmUnlink(contact)}
                  >
                    {t("unlink")}
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            </Group>
          ))}
        </Stack>
      )}

      <AddContactModal customerId={customerId} opened={addOpened} onClose={add.close} />
      <EditRoleModal
        customerId={customerId}
        contact={editingLink}
        onClose={() => setEditingLink(null)}
      />
    </Card>
  );
}

function AddContactModal({
  customerId,
  opened,
  onClose,
}: Readonly<{ customerId: number; opened: boolean; onClose: () => void }>) {
  const t = useTranslations("contacts.card");
  const addContact = useAddCustomerContact(customerId);

  const [mode, setMode] = useState<"new" | "existing">("new");
  const [role, setRole] = useState("");
  const [isPrimary, setIsPrimary] = useState(false);
  const [selectedContactId, setSelectedContactId] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, 250);

  const { data: searchResult } = useContacts(
    { search: debouncedSearch || undefined, pageSize: 20 },
    { enabled: opened && mode === "existing" },
  );

  const form = useContactForm();

  const handleClose = () => {
    form.reset();
    setRole("");
    setIsPrimary(false);
    setSelectedContactId(null);
    setSearch("");
    setMode("new");
    onClose();
  };

  const handleSuccess = () => {
    notifications.show({ color: "green", message: t("added") });
    handleClose();
  };

  const notifyError = (error: Error) =>
    notifications.show({ color: "red", message: error.message });

  const linkFields = { role: role.trim() || null, isPrimary };

  const submitNew = form.onSubmit((values) => {
    addContact.mutate(
      { contact: toContactInput(values), ...linkFields },
      { onSuccess: handleSuccess, onError: notifyError },
    );
  });

  const submitExisting = () => {
    if (!selectedContactId) return;
    addContact.mutate(
      { contactId: Number(selectedContactId), ...linkFields },
      { onSuccess: handleSuccess, onError: notifyError },
    );
  };

  return (
    <Modal opened={opened} onClose={handleClose} title={t("addTitle")} size="lg">
      <Stack gap="sm">
        <SegmentedControl
          fullWidth
          value={mode}
          onChange={(value) => setMode(value as "new" | "existing")}
          data={[
            { value: "new", label: t("modeNew") },
            { value: "existing", label: t("modeExisting") },
          ]}
        />

        {mode === "existing" && (
          <Select
            label={t("searchLabel")}
            placeholder={t("searchPlaceholder")}
            searchable
            clearable
            searchValue={search}
            onSearchChange={setSearch}
            value={selectedContactId}
            onChange={setSelectedContactId}
            nothingFoundMessage={t("searchEmpty")}
            data={(searchResult?.items ?? []).map((contact) => ({
              value: String(contact.id),
              label: contact.email ? `${contact.name} (${contact.email})` : contact.name,
            }))}
          />
        )}

        {mode === "new" ? (
          <form onSubmit={submitNew}>
            <Stack gap="sm">
              <ContactFields form={form} />
              <LinkFields
                role={role}
                onRoleChange={setRole}
                isPrimary={isPrimary}
                onPrimaryChange={setIsPrimary}
              />
              <Group justify="flex-end" mt="xs">
                <Button type="submit" loading={addContact.isPending}>
                  {t("addSubmit")}
                </Button>
              </Group>
            </Stack>
          </form>
        ) : (
          <>
            <LinkFields
              role={role}
              onRoleChange={setRole}
              isPrimary={isPrimary}
              onPrimaryChange={setIsPrimary}
            />
            <Group justify="flex-end" mt="xs">
              <Button
                loading={addContact.isPending}
                disabled={!selectedContactId}
                onClick={submitExisting}
              >
                {t("linkSubmit")}
              </Button>
            </Group>
          </>
        )}
      </Stack>
    </Modal>
  );
}

function LinkFields({
  role,
  onRoleChange,
  isPrimary,
  onPrimaryChange,
}: Readonly<{
  role: string;
  onRoleChange: (value: string) => void;
  isPrimary: boolean;
  onPrimaryChange: (value: boolean) => void;
}>) {
  const t = useTranslations("contacts.card");
  return (
    <>
      <TextInput
        label={t("roleLabel")}
        placeholder={t("rolePlaceholder")}
        value={role}
        onChange={(event) => onRoleChange(event.currentTarget.value)}
      />
      <Checkbox
        label={t("primaryLabel")}
        checked={isPrimary}
        onChange={(event) => onPrimaryChange(event.currentTarget.checked)}
      />
    </>
  );
}

function EditRoleModal({
  customerId,
  contact,
  onClose,
}: Readonly<{ customerId: number; contact: CustomerContactItem | null; onClose: () => void }>) {
  const t = useTranslations("contacts.card");
  const updateLink = useUpdateCustomerContact(customerId);
  const [role, setRole] = useState("");

  // Sync the field when a new contact is selected for editing.
  const [lastContactId, setLastContactId] = useState<number | null>(null);
  if (contact && contact.id !== lastContactId) {
    setLastContactId(contact.id);
    setRole(contact.role ?? "");
  }

  const handleSave = () => {
    if (!contact) return;
    updateLink.mutate(
      { contactId: contact.id, input: { role: role.trim() || null } },
      {
        onSuccess: onClose,
        onError: (error) => notifications.show({ color: "red", message: error.message }),
      },
    );
  };

  return (
    <Modal opened={contact !== null} onClose={onClose} title={t("editRoleTitle")}>
      <Stack gap="sm">
        <TextInput
          label={t("roleLabel")}
          placeholder={t("rolePlaceholder")}
          value={role}
          onChange={(event) => setRole(event.currentTarget.value)}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={updateLink.isPending} onClick={handleSave}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
