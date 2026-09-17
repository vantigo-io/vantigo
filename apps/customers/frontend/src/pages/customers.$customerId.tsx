import { Badge, Button, Group, Stack, Text } from "@mantine/core";
import { IconPencil } from "@tabler/icons-react";
import { useSuspenseQuery } from "@tanstack/react-query";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";

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

/**
 * The customer page's header. `actions` lets the host append entries that
 * lead out of the page (the Communications inbox, say) next to the
 * customer's own actions, without this package knowing about other modules.
 */
export const CustomerDetailHeader = ({ customerId, actions }: { customerId: number; actions?: ReactNode }) => {
  const { t, formatters } = useI18n("customers");
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const [modalState, setModalState] = useState<CustomerModalState | null>(null);

  const { data: identity } = useSuspenseQuery(legalIdentityQueryOptions(customerId));

  return (
    <Stack gap="lg">
      <Stack gap="xs">
        <PageHeader
          breadcrumbs={[{ label: t("customers"), to: "/customers" }, { label: customer.name }]}
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
              {actions}
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
