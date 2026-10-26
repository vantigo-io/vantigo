import { Alert, Badge, Button, Card, Group, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { IconAlertCircle, IconUserOff } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { myPlanningQueryOptions, type PlanningMe } from "../api/me";
import type { PlanningProjectRef } from "../api/projects";
import { NotFoundError } from "../api/request";
import { MAX_OPEN_WISHES, type PlanningWishKind } from "../api/wishes";
import { WishDialog } from "../components/wish-forms";
import { useWishText, useWithdrawWish } from "../components/wish-hooks";
import { usePlanningFormat } from "../lib/format";

/**
 * My planning (design S19, §7), phone first: the caller's own confirmed
 * bookings from today on, one card each with the project, the role, the
 * period and the load. A tentative row is the planner's draft and is never
 * here. A caller Staff links to no person is told why there is nothing.
 * Under the bookings, *My wishes* (S21, delivery C): the open wishes with
 * their seen stamp, each to withdraw, and the two ways to file one.
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
          aria-label={t("allocations")}
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
      {me.data && <MyWishes me={me.data} />}
    </Stack>
  );
};

/**
 * The distinct projects of the confirmed bookings shown, in their order, less
 * those the person already wishes off: what an off wish may name.
 */
const projectsOf = (me: PlanningMe): PlanningProjectRef[] => {
  const seen = new Set<number>(
    me.wishes.flatMap((wish) => (wish.kind === "off" && wish.project ? [wish.project.id] : [])),
  );
  return me.allocations.flatMap(({ project }) => {
    if (seen.has(project.id)) return [];
    seen.add(project.id);
    return [project];
  });
};

/**
 * My wishes (S21): what the caller wishes, open, oldest first, each with
 * whether a planner has seen it, and *Withdraw*; then the two ways to file
 * one — off a project of a booking shown, or for something new, which needs
 * no booking.
 */
const MyWishes = ({ me }: { me: PlanningMe }) => {
  const format = usePlanningFormat();
  const { t } = format;
  const wishText = useWishText();
  const withdraw = useWithdrawWish();
  const [filing, setFiling] = useState<PlanningWishKind>();
  const projects = projectsOf(me);
  const atLimit = me.wishes.length >= MAX_OPEN_WISHES;

  return (
    <Card withBorder padding="md" radius="md">
      <Stack gap="sm">
        <Stack gap={2}>
          <Title order={2} size="h4">
            {t("myWishes")}
          </Title>
          <Text size="sm" c="dimmed">
            {t("myWishesDescription")}
          </Text>
        </Stack>
        {me.wishes.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noOpenWishes")}
          </Text>
        ) : (
          <Stack component="ul" gap="xs" m={0} p={0} style={{ listStyle: "none" }} aria-label={t("myWishes")}>
            {me.wishes.map((wish) => (
              <Group component="li" key={wish.id} justify="space-between" wrap="nowrap" gap="sm">
                <Stack gap={0} style={{ minWidth: 0 }}>
                  <Text size="sm" fw={500}>
                    {wishText(wish)}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {wish.seenAt ? t("seenByPlanner", { date: format.stamp(wish.seenAt) }) : t("notSeenYet")}
                  </Text>
                </Stack>
                <Button
                  size="xs"
                  variant="subtle"
                  color="red"
                  aria-label={t("withdrawWishNamed", { wish: wishText(wish) })}
                  onClick={() => withdraw(wish)}
                >
                  {t("withdraw")}
                </Button>
              </Group>
            ))}
          </Stack>
        )}
        <Stack gap="xs" align="flex-start">
          <Button variant="light" disabled={atLimit || projects.length === 0} onClick={() => setFiling("off")}>
            {t("wishOffButton")}
          </Button>
          {!atLimit && projects.length === 0 && (
            <Text size="xs" c="dimmed">
              {t("offWishNeedsBooking")}
            </Text>
          )}
          <Button variant="light" disabled={atLimit} onClick={() => setFiling("new")}>
            {t("wishNewButton")}
          </Button>
          {atLimit && (
            <Text size="xs" c="dimmed">
              {t("refusal.wish_limit")}
            </Text>
          )}
        </Stack>
      </Stack>
      <WishDialog kind={filing} onClose={() => setFiling(undefined)} projects={projects} />
    </Card>
  );
};
