"use client";

import {
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  Loader,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconArchive, IconArrowLeft, IconPencil } from "@tabler/icons-react";
import type { Customer } from "@vantigo/customers/database/schema/customers";
import {
  useArchiveCustomer,
  useCustomer,
  useUpdateCustomer,
} from "@vantigo/customers/lib/customers/hooks";
import { countryFlag, countryName } from "@vantigo/customers/lib/i18n/country";
import Link from "next/link";
import { useFormatter, useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import { CustomerStatusBadge, EditCustomerModal, LegalTypeBadge } from "../customer-modals";

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

export function CustomerDetail({ customerId }: Readonly<{ customerId: number }>) {
  const t = useTranslations("customers");
  const format = useFormatter();
  const locale = useLocale();
  const { data: customer, isLoading, isError } = useCustomer(customerId);
  const archiveCustomer = useArchiveCustomer();
  const updateCustomer = useUpdateCustomer();
  const [editing, setEditing] = useState<Customer | null>(null);

  if (isLoading) {
    return (
      <Group justify="center" py="xl">
        <Loader />
      </Group>
    );
  }

  if (isError || !customer) {
    return (
      <Stack align="flex-start">
        <Title order={2}>{t("detail.notFoundTitle")}</Title>
        <Text c="dimmed">{t("detail.notFoundMessage", { id: customerId })}</Text>
        <Anchor component={Link} href="/customers">
          {t("detail.backToList")}
        </Anchor>
      </Stack>
    );
  }

  const confirmArchive = () => {
    modals.openConfirmModal({
      title: t("archiveConfirmTitle"),
      children: <Text size="sm">{t("archiveConfirmMessage", { name: customer.legalName })}</Text>,
      labels: { confirm: t("archiveConfirm"), cancel: t("archiveCancel") },
      confirmProps: { color: "red" },
      onConfirm: () => {
        archiveCustomer.mutate(customer.id, {
          onSuccess: () => {
            notifications.show({
              color: "green",
              title: t("notifications.archivedTitle"),
              message: t("notifications.archivedMessage", { name: customer.legalName }),
            });
          },
        });
      },
    });
  };

  const unarchive = () => {
    updateCustomer.mutate(
      { id: customer.id, input: { status: "active" } },
      {
        onSuccess: () => {
          notifications.show({
            color: "green",
            title: t("notifications.updatedTitle"),
            message: t("notifications.updatedMessage", { id: customer.id }),
          });
        },
      },
    );
  };

  return (
    <>
      <Anchor component={Link} href="/customers" size="sm">
        <Group gap={4}>
          <IconArrowLeft size={14} />
          {t("detail.backToList")}
        </Group>
      </Anchor>

      <Group justify="space-between" mt="sm" mb="lg" wrap="wrap">
        <Group gap="sm">
          <Title order={2}>{customer.legalName}</Title>
          <Badge variant="outline" color="gray">
            #{customer.id}
          </Badge>
          <LegalTypeBadge legalType={customer.legalType} />
          <CustomerStatusBadge status={customer.status} />
        </Group>
        <Group gap="sm">
          <Button
            variant="default"
            leftSection={<IconPencil size={16} />}
            onClick={() => setEditing(customer)}
          >
            {t("detail.edit")}
          </Button>
          {customer.status === "active" ? (
            <Button
              color="red"
              variant="light"
              leftSection={<IconArchive size={16} />}
              onClick={confirmArchive}
            >
              {t("table.archive")}
            </Button>
          ) : (
            <Button variant="light" onClick={unarchive} loading={updateCustomer.isPending}>
              {t("detail.unarchive")}
            </Button>
          )}
        </Group>
      </Group>

      <Card withBorder radius="md" p="lg">
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="lg">
          <Field label={t("form.legalName")} value={customer.legalName} />
          <Field label={t("form.legalType")} value={t(`legalType.${customer.legalType}`)} />
          <Field
            label={t("form.legalId")}
            value={
              <span title={countryName(customer.legalCountry, locale)}>
                {countryFlag(customer.legalCountry)} {customer.legalId}
              </span>
            }
          />
          <Field
            label={t("form.email")}
            value={
              customer.email ? (
                <Anchor href={`mailto:${customer.email}`}>{customer.email}</Anchor>
              ) : null
            }
          />
          <Field
            label={t("form.phone")}
            value={
              customer.phone ? (
                <Anchor href={`tel:${customer.phone}`}>{customer.phone}</Anchor>
              ) : null
            }
          />
          <Field
            label={t("detail.createdAt")}
            value={format.dateTime(new Date(customer.createdAt), {
              dateStyle: "medium",
              timeStyle: "short",
            })}
          />
        </SimpleGrid>

        <Stack mt="lg" gap={2}>
          <Field label={t("form.notes")} value={customer.notes} />
        </Stack>
      </Card>

      <EditCustomerModal customer={editing} onClose={() => setEditing(null)} />
    </>
  );
}
