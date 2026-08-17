import { Anchor, Breadcrumbs, Button, Card, Group, Stack, Text, Title } from "@mantine/core";
import { IconPencil } from "@tabler/icons-react";
import { useSuspenseQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
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
  const { t } = useI18n("customers");
  const { tenantSlug } = useParams({ strict: false });
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
          description={t("customerDetailsDescription")}
          actions={
            <Group gap="sm">
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

        {identity ? (
          <Stack gap="xs">
            <Title order={3}>{t("legalIdentity")}</Title>
            <Group gap="xs">
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
          </Stack>
        ) : (
          <Card withBorder padding="sm">
            <Text size="sm" c="dimmed">
              {t("legalIdentityUnavailable")}
            </Text>
          </Card>
        )}
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
