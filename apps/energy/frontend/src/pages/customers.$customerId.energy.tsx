import { Anchor, Breadcrumbs, Button, Card, Center, Loader, Stack, Text, Title } from "@mantine/core";
import { IconBolt, IconPlus } from "@tabler/icons-react";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { customerQueryOptions } from "../api/customer-details";
import { customerMeteringPointsQueryOptions } from "../api/energy";
import { AttachMeteringPointModal } from "./-attach-metering-point-modal";
import { CustomerMeterCard } from "./-customer-meter-card";

export const CustomerEnergyPage = () => {
  const { customerId } = useParams({ strict: false }) as { customerId: number };
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const { data: meters, isPending } = useQuery(customerMeteringPointsQueryOptions(customerId));
  const [attachOpen, setAttachOpen] = useState(false);
  return (
    <Stack gap="lg">
      <Breadcrumbs>
        <Anchor href={`/customers/${customerId}`} size="sm">
          Customers
        </Anchor>
        <Text size="sm">{customer.name}</Text>
      </Breadcrumbs>
      <PageHeader
        eyebrow="Customer energy"
        title={
          <>
            <IconBolt size={28} /> Energy
          </>
        }
        description={`Metering points and consumption for ${customer.name}.`}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setAttachOpen(true)}>
            Attach metering point
          </Button>
        }
      />
      <AttachMeteringPointModal customerId={customerId} opened={attachOpen} onClose={() => setAttachOpen(false)} />
      {isPending && (
        <Center py="xl">
          <Loader />
        </Center>
      )}
      {meters?.length
        ? meters.map((item) => <CustomerMeterCard key={item.meteringPoint.id} item={item} customerId={customerId} />)
        : !isPending && (
            <Card withBorder>
              <Stack align="center">
                <Title order={3}>No metering points</Title>
                <Text c="dimmed">Attach a metering point to start tracking consumption.</Text>
              </Stack>
            </Card>
          )}
    </Stack>
  );
};
