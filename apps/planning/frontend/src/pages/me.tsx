import { Alert, Badge, Card, Group, SimpleGrid, Stack, Text } from "@mantine/core";
import { IconAlertCircle, IconUserOff } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { myPlanningQueryOptions } from "../api/me";
import { NotFoundError } from "../api/request";
import { usePlanningFormat } from "../lib/format";

/**
 * My planning (design S19, §7), phone first: the caller's own confirmed
 * bookings from today on, one card each with the project, the role, the
 * period and the load. A tentative row is the planner's draft and is never
 * here. A caller Staff links to no person is told why there is nothing.
 */
export const MyPlanningPage = () => {
  const format = usePlanningFormat();
  const { t } = format;
  const me = useQuery(myPlanningQueryOptions());
  const noPerson = me.error instanceof NotFoundError;

  return (
    <Stack gap="lg">
      <PageHeader title={t("myPlanningTitle")} description={t("myPlanningDescription")} />
      {noPerson && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState icon={IconUserOff} title={t("noPersonTitle")} description={t("refusal.no_person")} />
        </Card>
      )}
      {me.isError && !noPerson && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMyPlanning")}>
          {me.error.message}
        </Alert>
      )}
      {me.isPending && <ContentSkeleton rows={3} rowHeight={72} />}
      {me.data && me.data.allocations.length === 0 && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState title={t("noBookingsAhead")} description={t("noBookingsAheadDescription")} />
        </Card>
      )}
      {me.data && me.data.allocations.length > 0 && (
        <SimpleGrid
          cols={{ base: 1, sm: 2, lg: 3 }}
          spacing="md"
          component="ul"
          style={{ listStyle: "none", padding: 0, margin: 0 }}
        >
          {me.data.allocations.map((allocation) => (
            <Card
              component="li"
              key={allocation.id}
              withBorder
              padding="md"
              radius="md"
              aria-label={format.projectName(allocation.project)}
            >
              <Stack gap={4}>
                <Group justify="space-between" wrap="nowrap" align="start">
                  <Text fw={600}>{format.projectName(allocation.project)}</Text>
                  <Badge variant="light" size="sm">
                    {format.load(allocation.load, allocation.loadUnit)}
                  </Badge>
                </Group>
                {allocation.title && <Text size="sm">{allocation.title}</Text>}
                <Text size="sm" c="dimmed">
                  {format.range(allocation.fromDate, allocation.toDate)}
                </Text>
              </Stack>
            </Card>
          ))}
        </SimpleGrid>
      )}
    </Stack>
  );
};
