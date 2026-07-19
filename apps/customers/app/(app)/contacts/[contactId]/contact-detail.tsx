"use client";

import {
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  Loader,
  Modal,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconArrowLeft, IconPencil, IconTrash } from "@tabler/icons-react";
import { ContactForm } from "@vantigo/customers/lib/contacts/contact-form";
import {
  useContact,
  useDeleteContact,
  useUpdateContact,
} from "@vantigo/customers/lib/contacts/hooks";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useFormatter, useTranslations } from "next-intl";

function Field({ label, value }: Readonly<{ label: string; value: React.ReactNode }>) {
  return (
    <div>
      <Text size="xs" c="dimmed" fw={700} tt="uppercase">
        {label}
      </Text>
      <Text size="sm" mt={2}>
        {value || "–"}
      </Text>
    </div>
  );
}

export function ContactDetail({ contactId }: Readonly<{ contactId: number }>) {
  const t = useTranslations("contacts");
  const format = useFormatter();
  const router = useRouter();
  const { data: contact, isLoading, isError } = useContact(contactId);
  const updateContact = useUpdateContact();
  const deleteContact = useDeleteContact();
  const [editOpened, edit] = useDisclosure(false);

  if (isLoading) {
    return (
      <Group justify="center" py="xl">
        <Loader />
      </Group>
    );
  }

  if (isError || !contact) {
    return (
      <Stack align="flex-start">
        <Title order={2}>{t("detail.notFoundTitle")}</Title>
        <Anchor component={Link} href="/contacts">
          {t("detail.backToList")}
        </Anchor>
      </Stack>
    );
  }

  const confirmDelete = () => {
    modals.openConfirmModal({
      title: t("detail.deleteTitle"),
      children: (
        <Text size="sm">
          {contact.customers.length > 0
            ? t("detail.deleteMessageLinked", {
                name: contact.name,
                count: contact.customers.length,
              })
            : t("detail.deleteMessage", { name: contact.name })}
        </Text>
      ),
      labels: { confirm: t("detail.deleteConfirm"), cancel: t("detail.deleteCancel") },
      confirmProps: { color: "red" },
      onConfirm: () =>
        deleteContact.mutate(contact.id, {
          onSuccess: () => {
            notifications.show({
              color: "green",
              message: t("notifications.deleted", { name: contact.name }),
            });
            router.push("/contacts");
          },
          onError: (error) => notifications.show({ color: "red", message: error.message }),
        }),
    });
  };

  return (
    <>
      <Anchor component={Link} href="/contacts" size="sm">
        <Group gap={4}>
          <IconArrowLeft size={14} />
          {t("detail.backToList")}
        </Group>
      </Anchor>

      <Group justify="space-between" mt="sm" mb="lg" wrap="wrap">
        <Title order={2}>{contact.name}</Title>
        <Group gap="sm">
          <Button variant="default" leftSection={<IconPencil size={16} />} onClick={edit.open}>
            {t("detail.edit")}
          </Button>
          <Button
            color="red"
            variant="light"
            leftSection={<IconTrash size={16} />}
            onClick={confirmDelete}
          >
            {t("detail.delete")}
          </Button>
        </Group>
      </Group>

      <Card withBorder radius="md" p="lg">
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="lg">
          <Field
            label={t("form.email")}
            value={
              contact.email ? (
                <Anchor href={`mailto:${contact.email}`}>{contact.email}</Anchor>
              ) : null
            }
          />
          <Field
            label={t("form.phone")}
            value={
              contact.phone ? <Anchor href={`tel:${contact.phone}`}>{contact.phone}</Anchor> : null
            }
          />
          <Field
            label={t("detail.createdAt")}
            value={format.dateTime(new Date(contact.createdAt), {
              dateStyle: "medium",
              timeStyle: "short",
            })}
          />
        </SimpleGrid>

        <Stack mt="lg" gap={2}>
          <Field label={t("form.notes")} value={contact.notes} />
        </Stack>
      </Card>

      <Card withBorder radius="md" p="lg" mt="lg">
        <Title order={4} mb="sm">
          {t("detail.linkedCustomers")}
        </Title>
        {contact.customers.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("detail.noLinkedCustomers")}
          </Text>
        ) : (
          <Stack gap="xs">
            {contact.customers.map((link) => (
              <Group key={link.customerId} gap="xs">
                <Anchor component={Link} href={`/customers/${link.customerId}`} size="sm" fw={500}>
                  {link.legalName}
                </Anchor>
                <Text size="xs" c="dimmed">
                  #{link.customerId}
                </Text>
                {link.role && (
                  <Text size="xs" c="dimmed">
                    · {link.role}
                  </Text>
                )}
                {link.isPrimary && (
                  <Badge size="xs" color="teal" variant="light">
                    {t("detail.primary")}
                  </Badge>
                )}
              </Group>
            ))}
          </Stack>
        )}
      </Card>

      <Modal opened={editOpened} onClose={edit.close} title={t("detail.editTitle")} size="lg">
        <ContactForm
          initialValues={contact}
          isSubmitting={updateContact.isPending}
          submitLabel={t("form.save")}
          onSubmit={(input) =>
            updateContact.mutate(
              { id: contact.id, input },
              {
                onSuccess: () => {
                  notifications.show({ color: "green", message: t("notifications.updated") });
                  edit.close();
                },
                onError: (error) => notifications.show({ color: "red", message: error.message }),
              },
            )
          }
        />
      </Modal>
    </>
  );
}
