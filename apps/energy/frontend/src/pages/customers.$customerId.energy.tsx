import { Button, Group, Stack } from "@mantine/core";
import { IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { customerConsumptionAggregateQueryOptions, customerMeteringPointsQueryOptions } from "../api/energy";
import { AttachMeteringPointModal } from "./-attach-metering-point-modal";
import { CustomerEnergyStats } from "./-customer-energy-stats";
import { CustomerMetersTable } from "./-customer-meters-table";
import "../i18n";

const lastTwelveMonths = (now: string) => {
  const from = new Date(now);
  from.setUTCFullYear(from.getUTCFullYear() - 1);
  return { from: from.toISOString(), to: now };
};

export const CustomerEnergyPanel = ({ customerId }: { customerId: number }) => {
  const { t } = useI18n("energy");
  // A stable per-mount "now" keeps the aggregate query key from changing every render.
  const [now] = useState(() => new Date().toISOString());
  const { from, to } = lastTwelveMonths(now);
  const { data: meters, isPending } = useQuery(customerMeteringPointsQueryOptions(customerId));
  const { data: aggregates } = useQuery(
    customerConsumptionAggregateQueryOptions(customerId, { from, to, resolution: "month" }),
  );
  const [attachOpen, setAttachOpen] = useState(false);
  return (
    <Stack gap="lg" mt="md">
      <CustomerEnergyStats meters={meters ?? []} aggregates={aggregates ?? []} />
      <Group justify="flex-end">
        <Button leftSection={<IconPlus size={16} />} onClick={() => setAttachOpen(true)}>
          {t("attachMeteringPoint")}
        </Button>
      </Group>
      <AttachMeteringPointModal customerId={customerId} opened={attachOpen} onClose={() => setAttachOpen(false)} />
      {isPending ? <ContentSkeleton rows={4} rowHeight={52} /> : <CustomerMetersTable meters={meters ?? []} />}
    </Stack>
  );
};
