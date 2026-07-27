import { Anchor, Breadcrumbs, Button, Center, Group, Stack, Text, Title } from "@mantine/core";
import { IconPencil, IconUserQuestion } from "@tabler/icons-react";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, notFound } from "@tanstack/react-router";
import { useState } from "react";

import { customerQueryOptions, NotFoundError } from "../api/customers";
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

const tooltips = {
  legalName: "The official name of the entity as registered in the public registry.",
  legalId:
    "The unique registration number identifying the entity in its country's public registry — in Norway, the organisation number from Enhetsregisteret.",
  country: "The country whose public registry the entity is registered in.",
  type: "Whether the customer is registered as a business or a private person.",
  manualSource: "This information was entered manually.",
  customerId: "The unique id identifying the customer within Vantigo.",
};

const CustomerDetailsPage = () => {
  const { customerId } = Route.useParams();
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
        <Group justify="space-between">
          <Group gap="sm">
            <Title order={2}>{customer.name}</Title>
            <CopyableBadge variant="light" size="lg" tooltip={tooltips.customerId} copyValue={String(customer.id)}>
              #{customer.id}
            </CopyableBadge>
          </Group>
          <Button
            variant="default"
            leftSection={<IconPencil size={16} />}
            onClick={() => setModalState({ mode: "edit", customer })}
          >
            Edit customer
          </Button>
        </Group>

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
    </Stack>
  );
};

const CustomerNotFound = () => (
  <Center py="xl">
    <Stack align="center" gap="sm">
      <IconUserQuestion size={48} stroke={1.2} />
      <Title order={3}>Customer not found</Title>
      <Text c="dimmed">The customer you are looking for does not exist.</Text>
      <Button component={Link} to="/customers" variant="light">
        Back to customers
      </Button>
    </Stack>
  </Center>
);

export const Route = createFileRoute("/customers/$customerId")({
  params: {
    parse: ({ customerId }) => {
      const id = Number(customerId);
      if (!Number.isInteger(id) || id < 1) {
        throw new Error(`Invalid customer id: ${customerId}`);
      }
      return { customerId: id };
    },
    stringify: ({ customerId }) => ({ customerId: String(customerId) }),
  },
  loader: async ({ context: { queryClient }, params: { customerId } }) => {
    try {
      await queryClient.ensureQueryData(customerQueryOptions(customerId));
    } catch (error) {
      if (error instanceof NotFoundError) {
        throw notFound();
      }
      throw error;
    }
  },
  notFoundComponent: CustomerNotFound,
  component: CustomerDetailsPage,
});
