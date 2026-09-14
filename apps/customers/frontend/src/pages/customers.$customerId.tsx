import { Anchor, Badge, Breadcrumbs, Button, Group, Stack, Text } from "@mantine/core";
import { IconPencil } from "@tabler/icons-react";
import { useSuspenseQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";

import { customerQueryOptions, legalIdentityQueryOptions } from "../api/customers";
import {
  CopyableBadge,
  LegalCountryBadge,
  LegalSourceBadge,
  LegalTypeBadge,
  LegalValueBadge,
} from "../components/legal-badges";
import { getLegalSource } from "../lib/legal-sources";
import { CustomerContactsCard } from "./-customer-contacts-card";
import { CustomerFormModal, type CustomerModalState } from "./-customer-form-modal";
import { CustomerTimeline } from "./-customer-timeline";
import "../i18n";

export const CustomerDetailHeader = ({ customerId }: { customerId: number }) => {
  const { t, formatters } = useI18n("customers");
  // The $tenantSlug route param no longer exists (task 3 of the frontend
  // de-tenanting plan collapsed it); task 7 owns removing this idiom.
  const tenantSlug: string | undefined = undefined;
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const [modalState, setModalState] = useState<CustomerModalState | null>(null);

  const { data: identity } = useSuspenseQuery(legalIdentityQueryOptions(customerId));

  return (
    <Stack gap="lg">
      <Breadcrumbs>
        <Anchor
          component={Link}
          to={`${tenantSlug ? `/${encodeURIComponent(tenantSlug)}` : ""}/customers` as never}
          size="sm"
        >
          {t("customers")}
        </Anchor>
        <Text size="sm">{customer.name}</Text>
      </Breadcrumbs>

      <Stack gap="xs">
        <PageHeader
          eyebrow={t("customers")}
          title={customer.name}
          description={
            identity ? (
              <Group gap="xs" mt={4}>
                <LegalValueBadge type={identity.type} tooltip={t("tooltipLegalName")} copyable>
                  {identity.name}
                </LegalValueBadge>
                <LegalValueBadge type={identity.type} tooltip={t("tooltipLegalId")} copyable>
                  {identity.id}
                </LegalValueBadge>
                <LegalCountryBadge country={identity.country} tooltip={t("tooltipCountry")} copyable />
                <LegalTypeBadge type={identity.type} tooltip={t("tooltipType")} copyable />
                <LegalSourceBadge
                  source={identity.source}
                  legalId={identity.id}
                  tooltip={getLegalSource(identity.source).logo ? undefined : t("tooltipManualSource")}
                />
              </Group>
            ) : (
              t("legalIdentityUnavailable")
            )
          }
          actions={
            <Group gap="sm">
              <Badge variant="light" size="lg" color={customer.status === "active" ? "teal" : "gray"}>
                {customer.status === "active"
                  ? t("statusActive")
                  : customer.status === "archived"
                    ? t("statusArchived")
                    : t("statusDisabled")}
              </Badge>
              <CopyableBadge variant="light" size="lg" tooltip={t("customerIdTooltip")} copyValue={String(customer.id)}>
                #{customer.id}
              </CopyableBadge>
              <Button
                variant="default"
                leftSection={<IconPencil size={16} />}
                onClick={() => setModalState({ mode: "edit", customer })}
              >
                {t("editCustomer")}
              </Button>
            </Group>
          }
        />
        <Text size="sm" c="dimmed">
          {t("createdOnDate", { date: formatters.formatDate(customer.createdAt) })}
          {" · "}
          {t("updatedOnDate", { date: formatters.formatDate(customer.updatedAt) })}
        </Text>
      </Stack>

      <CustomerFormModal state={modalState} onClose={() => setModalState(null)} />
    </Stack>
  );
};

export const CustomerOverview = ({ customerId }: { customerId: number }) => (
  <Stack gap="lg">
    <CustomerContactsCard customerId={customerId} />
    <CustomerTimeline customerId={customerId} />
  </Stack>
);
