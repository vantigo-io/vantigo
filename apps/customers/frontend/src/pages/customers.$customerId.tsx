import { Anchor, Breadcrumbs, Button, Group, Stack, Text } from "@mantine/core";
import { IconPencil } from "@tabler/icons-react";
import { useSuspenseQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";

import { customerQueryOptions } from "../api/customers";
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

const tooltips = {
  legalName: "The official name of the entity as registered in the public registry.",
  legalId:
    "The unique registration number identifying the entity in its country's public registry — in Norway, the organisation number from Enhetsregisteret.",
  country: "The country whose public registry the entity is registered in.",
  type: "Whether the customer is registered as a business or a private person.",
  manualSource: "This information was entered manually.",
  customerId: "The unique id identifying the customer within Vantigo.",
};

export const CustomerDetailsPage = () => {
  const { customerId } = useParams({ strict: false }) as { customerId: number };
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const [modalState, setModalState] = useState<CustomerModalState | null>(null);

  const identity = customer.identity;

  return (
    <Stack gap="lg">
      <Breadcrumbs>
        <Anchor component={Link} to="/customers" size="sm">
          Customers
        </Anchor>
        <Text size="sm">{customer.name}</Text>
      </Breadcrumbs>

      <Stack gap="xs">
        <PageHeader
          eyebrow="Customers"
          title={customer.name}
          description="Contacts, details, and activity for this customer."
          actions={
            <Group gap="sm">
              <CopyableBadge variant="light" size="lg" tooltip={tooltips.customerId} copyValue={String(customer.id)}>
                #{customer.id}
              </CopyableBadge>
              <Button
                variant="default"
                leftSection={<IconPencil size={16} />}
                onClick={() => setModalState({ mode: "edit", customer })}
              >
                Edit customer
              </Button>
            </Group>
          }
        />

        {identity && (
          <Group gap="xs">
            <LegalValueBadge type={identity.type} tooltip={tooltips.legalName} copyable>
              {identity.name}
            </LegalValueBadge>
            <LegalValueBadge type={identity.type} tooltip={tooltips.legalId} copyable>
              {identity.id}
            </LegalValueBadge>
            <LegalCountryBadge country={identity.country} tooltip={tooltips.country} copyable />
            <LegalTypeBadge type={identity.type} tooltip={tooltips.type} copyable />
            <LegalSourceBadge
              source={identity.source}
              legalId={identity.id}
              tooltip={getLegalSource(identity.source).logo ? undefined : tooltips.manualSource}
            />
          </Group>
        )}
      </Stack>

      <CustomerFormModal state={modalState} onClose={() => setModalState(null)} />

      <CustomerContactsCard customerId={customer.id} />
      <CustomerTimeline customerId={customer.id} />
    </Stack>
  );
};
