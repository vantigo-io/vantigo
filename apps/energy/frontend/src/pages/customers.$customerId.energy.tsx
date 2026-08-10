import { Button, Card, Center, Group, Loader, Stack, Text, Title } from "@mantine/core";
import { IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { customerMeteringPointsQueryOptions } from "../api/energy";
import { AttachMeteringPointModal } from "./-attach-metering-point-modal";
import { CustomerMeterCard } from "./-customer-meter-card";

export const CustomerEnergyPanel = ({ customerId }: { customerId: number }) => {
  const { data: meters, isPending } = useQuery(customerMeteringPointsQueryOptions(customerId));
  const [attachOpen, setAttachOpen] = useState(false);
  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Title order={3}>Energy</Title>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setAttachOpen(true)}>
          Attach metering point
        </Button>
      </Group>
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
