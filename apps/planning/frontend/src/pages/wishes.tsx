import { Alert, Anchor, Badge, Button, Card, Group, Stack, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { planningMetaQueryOptions } from "../api/meta";
import { PLANNING_QUERY_KEY } from "../api/request";
import { markWishSeen, type PlanningWish, resolveWish, wishesQueryOptions } from "../api/wishes";
import { useWishRefusal, useWishText } from "../components/wish-hooks";
import { NONE, usePlanningFormat } from "../lib/format";
import { personTimelineLinkOptions, projectPlanLinkOptions } from "../lib/routes";

/**
 * The planners' open wishes (design S21, §7), oldest first: who wishes what —
 * off a project or for something new — from when, filed when, and whether a
 * planner has seen it; a wish nobody has seen is marked *New*. *Mark seen*
 * stamps it; *Resolve* deletes it once handled, the record keeping the kind,
 * the project and the date. `planning:manage`'s. When Staff answers nobody
 * to the caller (`workforceVisible: false`) the people read *A colleague*,
 * and a note above the list says why.
 */
export const WishesPage = () => {
  const format = usePlanningFormat();
  const { t } = format;
  const queryClient = useQueryClient();
  const wishes = useQuery(wishesQueryOptions());
  const meta = useQuery(planningMetaQueryOptions());
  const wishText = useWishText();
  const refusal = useWishRefusal();

  const failed = async (error: Error) => {
    notifications.show({ color: "red", title: t("couldNotSave"), message: refusal(error) });
    await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
  };
  const seen = useMutation({
    mutationFn: (wish: PlanningWish) => markWishSeen(wish.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("wishMarkedSeen"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
    onError: failed,
  });
  const resolve = useMutation({
    mutationFn: (wish: PlanningWish) => resolveWish(wish.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("wishResolved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
    onError: failed,
  });

  /** The wish named for a row action: the person and what they wish. */
  const named = (wish: PlanningWish) => `${format.personName(wish.person)}, ${wishText(wish)}`;

  return (
    <Stack gap="lg">
      <PageHeader title={t("wishesTitle")} description={t("wishesDescription")} />
      {meta.data?.workforceVisible === false && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState title={t("workforceHiddenTitle")} description={t("workforceHiddenDescription")} />
        </Card>
      )}
      <Card withBorder padding="sm" radius="md">
        {wishes.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadWishes")}>
            {wishes.error.message}
          </Alert>
        )}
        {wishes.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
        {wishes.data && wishes.data.length === 0 && (
          <EmptyState title={t("noOpenWishes")} description={t("noOpenWishesDescription")} />
        )}
        {wishes.data && wishes.data.length > 0 && (
          <Table.ScrollContainer minWidth={720}>
            <Table aria-label={t("wishesTitle")} verticalSpacing="xs" highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("person")}</Table.Th>
                  <Table.Th>{t("kind")}</Table.Th>
                  <Table.Th>{t("project")}</Table.Th>
                  <Table.Th>{t("wishFrom")}</Table.Th>
                  <Table.Th>{t("filed")}</Table.Th>
                  <Table.Th>{t("seen")}</Table.Th>
                  <Table.Th>{t("rowActions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {wishes.data.map((wish) => {
                  const project = wish.project;
                  return (
                    <Table.Tr key={wish.id}>
                      <Table.Td>
                        {wish.person.displayName === null ? (
                          <Text size="sm">{format.personName(wish.person)}</Text>
                        ) : (
                          <Anchor
                            size="sm"
                            fw={500}
                            renderRoot={(props) => <Link {...personTimelineLinkOptions(wish.person.id)} {...props} />}
                          >
                            {wish.person.displayName}
                          </Anchor>
                        )}
                      </Table.Td>
                      <Table.Td>{t(`wishKindLabel_${wish.kind}`)}</Table.Td>
                      <Table.Td>
                        {project ? (
                          <Anchor
                            size="sm"
                            renderRoot={(props) => <Link {...projectPlanLinkOptions(project.id)} {...props} />}
                          >
                            {format.projectName(project)}
                          </Anchor>
                        ) : (
                          NONE
                        )}
                      </Table.Td>
                      <Table.Td>{format.date(wish.fromDate)}</Table.Td>
                      <Table.Td>{format.stamp(wish.createdAt)}</Table.Td>
                      <Table.Td>
                        {wish.seenAt ? (
                          t("seenOn", { date: format.stamp(wish.seenAt) })
                        ) : (
                          <Badge variant="filled" color="orange" size="sm">
                            {t("unseen")}
                          </Badge>
                        )}
                      </Table.Td>
                      <Table.Td>
                        <Group gap="xs" justify="flex-end" wrap="nowrap">
                          {!wish.seenAt && (
                            <Button
                              size="xs"
                              variant="light"
                              aria-label={t("markSeenFor", { wish: named(wish) })}
                              loading={seen.isPending && seen.variables?.id === wish.id}
                              onClick={() => seen.mutate(wish)}
                            >
                              {t("markSeen")}
                            </Button>
                          )}
                          <Button
                            size="xs"
                            variant="subtle"
                            aria-label={t("resolveFor", { wish: named(wish) })}
                            loading={resolve.isPending && resolve.variables?.id === wish.id}
                            onClick={() =>
                              modals.openConfirmModal({
                                title: t("resolveWishTitle"),
                                children: <Text size="sm">{t("resolveWishExplained")}</Text>,
                                labels: { confirm: t("resolve"), cancel: t("cancel") },
                                onConfirm: () => resolve.mutate(wish),
                              })
                            }
                          >
                            {t("resolve")}
                          </Button>
                        </Group>
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Card>
    </Stack>
  );
};
