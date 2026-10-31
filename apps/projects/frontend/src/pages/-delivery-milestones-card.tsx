import { ActionIcon, Badge, Box, Button, Card, Group, Menu, Progress, SimpleGrid, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconArrowDown,
  IconArrowUp,
  IconCheck,
  IconDiamond,
  IconDiamondFilled,
  IconDots,
  IconPencil,
  IconPlus,
  IconRotateDot,
  IconTrash,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type DeliveryMilestone,
  deleteDeliveryMilestone,
  deliveryMilestonesQueryOptions,
  moveDeliveryMilestone,
  setDeliveryMilestoneStatus,
} from "../api/delivery-milestones";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import { deliveryMilestoneStatusColor, deliveryMilestoneStatusLabelKey } from "../lib/delivery-milestones";
import { DeliveryMilestoneFormModal, type DeliveryMilestoneModalState } from "./-delivery-milestone-form-modal";

export interface DeliveryMilestonesCardProps {
  projectId: number;
  canContribute: boolean;
  selectedMilestoneId: number | null;
  onSelectMilestone: (milestoneId: number | null) => void;
}

export const DeliveryMilestonesCard = ({
  projectId,
  canContribute,
  selectedMilestoneId,
  onSelectMilestone,
}: DeliveryMilestonesCardProps) => {
  const { t } = useI18n("projects");
  const { data, isPending, isError } = useQuery(deliveryMilestonesQueryOptions(projectId));
  const [modalState, setModalState] = useState<DeliveryMilestoneModalState | null>(null);

  if (isError) return null;
  if (isPending) return <ContentSkeleton rows={2} rowHeight={36} />;

  const milestones = data?.milestones ?? [];

  return (
    <>
      <Card withBorder padding="md" radius="md">
        <Stack gap="sm">
          <Group justify="space-between" align="center" wrap="wrap">
            <Group gap="xs">
              <IconDiamond size={18} />
              <Text fw={600} size="sm">
                {t("deliveryMilestones")}
              </Text>
              <Text size="xs" c="dimmed">
                ({milestones.length})
              </Text>
            </Group>
            <Group gap="xs">
              {selectedMilestoneId !== null && (
                <Button size="xs" variant="subtle" color="gray" onClick={() => onSelectMilestone(null)}>
                  {t("allDeliveryMilestones")}
                </Button>
              )}
              {canContribute && (
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconPlus size={14} />}
                  onClick={() => setModalState({ mode: "create" })}
                >
                  {t("addDeliveryMilestone")}
                </Button>
              )}
            </Group>
          </Group>

          {milestones.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noDeliveryMilestones")}
            </Text>
          ) : (
            <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="sm">
              {milestones.map((milestone, idx) => (
                <MilestoneTile
                  key={milestone.id}
                  milestone={milestone}
                  isFirst={idx === 0}
                  isLast={idx === milestones.length - 1}
                  isSelected={selectedMilestoneId === milestone.id}
                  canContribute={canContribute}
                  onSelect={() => onSelectMilestone(selectedMilestoneId === milestone.id ? null : milestone.id)}
                  onEdit={() => setModalState({ mode: "edit", milestone })}
                />
              ))}
            </SimpleGrid>
          )}
        </Stack>
      </Card>

      <DeliveryMilestoneFormModal projectId={projectId} state={modalState} onClose={() => setModalState(null)} />
    </>
  );
};

interface MilestoneTileProps {
  milestone: DeliveryMilestone;
  isFirst: boolean;
  isLast: boolean;
  isSelected: boolean;
  canContribute: boolean;
  onSelect: () => void;
  onEdit: () => void;
}

const MilestoneTile = ({
  milestone,
  isFirst,
  isLast,
  isSelected,
  canContribute,
  onSelect,
  onEdit,
}: MilestoneTileProps) => {
  const { t, formatters } = useI18n("projects");
  const dates = useProjectDates();
  const queryClient = useQueryClient();

  const toggleStatus = useMutation({
    mutationFn: () =>
      setDeliveryMilestoneStatus(milestone.id, {
        status: milestone.status === "completed" ? "open" : "completed",
        revision: milestone.revision,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
    onError: (error) => {
      notifications.show({
        color: "red",
        title: t("couldNotSaveDeliveryMilestone"),
        message: error.message,
      });
    },
  });

  const move = useMutation({
    mutationFn: (direction: "up" | "down") => {
      const newPos = direction === "up" ? milestone.position - 1 : milestone.position + 1;
      return moveDeliveryMilestone(milestone.id, {
        position: newPos,
        revision: milestone.revision,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
    onError: (error) => {
      notifications.show({
        color: "red",
        title: t("couldNotSaveDeliveryMilestone"),
        message: error.message,
      });
    },
  });

  const remove = useMutation({
    mutationFn: () => deleteDeliveryMilestone(milestone.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({
        color: "teal",
        title: t("deliveryMilestoneDeleted"),
        message: milestone.name,
      });
    },
    onError: (error) => {
      notifications.show({
        color: "red",
        title: t("couldNotDeleteDeliveryMilestone"),
        message: error.message,
      });
    },
  });

  const confirmDelete = () => {
    modals.openConfirmModal({
      title: t("deleteDeliveryMilestoneTitle", { name: milestone.name }),
      children: <Text size="sm">{t("deleteDeliveryMilestoneWarning")}</Text>,
      labels: { confirm: t("deleteDeliveryMilestone"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });
  };

  const isCompleted = milestone.status === "completed";

  return (
    <Card
      withBorder
      padding="sm"
      radius="sm"
      style={{
        cursor: "pointer",
        borderColor: isSelected ? "var(--mantine-color-blue-filled)" : undefined,
        backgroundColor: isSelected ? "var(--mantine-color-blue-light)" : undefined,
      }}
      onClick={onSelect}
    >
      <Stack gap="xs">
        <Group justify="space-between" align="start" wrap="nowrap">
          <Group gap="xs" wrap="nowrap">
            {isCompleted ? (
              <IconDiamondFilled size={16} color="var(--mantine-color-teal-filled)" />
            ) : (
              <IconDiamond size={16} color="var(--mantine-color-blue-filled)" />
            )}
            <Text fw={600} size="sm" lineClamp={1}>
              {milestone.name}
            </Text>
          </Group>
          <Group gap={4} wrap="nowrap" onClick={(e) => e.stopPropagation()}>
            <Badge variant="light" size="xs" color={deliveryMilestoneStatusColor(milestone.status)}>
              {t(deliveryMilestoneStatusLabelKey(milestone.status))}
            </Badge>
            {canContribute && (
              <Menu position="bottom-end" shadow="md">
                <Menu.Target>
                  <ActionIcon
                    variant="subtle"
                    size="sm"
                    aria-label={t("milestoneActionsFor", { name: milestone.name })}
                  >
                    <IconDots size={14} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item
                    leftSection={isCompleted ? <IconRotateDot size={14} /> : <IconCheck size={14} />}
                    onClick={() => toggleStatus.mutate()}
                  >
                    {isCompleted ? t("markDeliveryMilestoneOpen") : t("markDeliveryMilestoneCompleted")}
                  </Menu.Item>
                  <Menu.Item leftSection={<IconPencil size={14} />} onClick={onEdit}>
                    {t("edit")}
                  </Menu.Item>
                  {!isFirst && (
                    <Menu.Item leftSection={<IconArrowUp size={14} />} onClick={() => move.mutate("up")}>
                      {t("moveMilestoneUp")}
                    </Menu.Item>
                  )}
                  {!isLast && (
                    <Menu.Item leftSection={<IconArrowDown size={14} />} onClick={() => move.mutate("down")}>
                      {t("moveMilestoneDown")}
                    </Menu.Item>
                  )}
                  <Menu.Divider />
                  <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={confirmDelete}>
                    {t("deleteDeliveryMilestone")}
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            )}
          </Group>
        </Group>

        {milestone.targetDate && (
          <Text size="xs" c="dimmed">
            {t("targetDate")}: {dates.day(milestone.targetDate)}
          </Text>
        )}

        <Box>
          <Group justify="space-between" mb={4}>
            <Text size="xs" c="dimmed">
              {t("deliveryMilestoneProgress", {
                completed: milestone.completedTasks,
                total: milestone.totalTasks,
                percent: milestone.progressPercent,
              })}
            </Text>
            {milestone.estimatedHoursTotal !== null &&
              milestone.estimatedHoursTotal !== undefined &&
              milestone.estimatedHoursTotal > 0 && (
                <Text size="xs" c="dimmed">
                  {formatters.formatNumber(milestone.estimatedHoursCompleted ?? 0)} /{" "}
                  {formatters.formatNumber(milestone.estimatedHoursTotal)} h
                </Text>
              )}
          </Group>
          <Progress value={milestone.progressPercent} size="xs" color={isCompleted ? "teal" : "blue"} />
        </Box>
      </Stack>
    </Card>
  );
};
