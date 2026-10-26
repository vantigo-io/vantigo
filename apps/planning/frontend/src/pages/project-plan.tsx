import { ActionIcon, Alert, Badge, Button, Card, Group, Menu, Stack, Table, Text, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDots, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { confirmAllocation, deleteAllocation, type PlanningAllocation } from "../api/allocations";
import { planningMetaQueryOptions } from "../api/meta";
import { projectPlanQueryOptions } from "../api/plan";
import { PLANNING_QUERY_KEY, refusalCode, ruleRefusals } from "../api/request";
import { AllocationBars } from "../components/allocation-bars";
import { type AllocationPreset, AllocationSheet } from "../components/allocation-sheet";
import { WeekHeader } from "../components/week-header";
import { usePlanningFormat } from "../lib/format";
import { BOARD_ROUTE_PATH } from "../lib/routes";
import type { WindowSearch } from "../lib/search";
import { mondayOf, today } from "../lib/weeks";
import { planWindow } from "../lib/window";

const WEEK_WIDTH = 80;

/** A project status's badge colour. */
const statusColor: Record<string, string> = {
  planned: "violet",
  active: "teal",
  "on-hold": "yellow",
  completed: "gray",
  cancelled: "red",
};

/**
 * One project's plan (design S11, §7): the project, the hours booked and
 * tentative each week, its allocations as bars and as a list a planner works
 * through — change, confirm, delete — and New allocation. `planning:view`'s.
 */
export const ProjectPlanPage = () => {
  const format = usePlanningFormat();
  const { t } = format;
  const { projectId } = useParams({ strict: false }) as { projectId: string };
  const search = useSearch({ strict: false }) as WindowSearch;
  const id = Number(projectId);
  const window = planWindow(search);
  const plan = useQuery(projectPlanQueryOptions(id, window));
  const meta = useQuery(planningMetaQueryOptions());
  const canManage = meta.data?.canManage === true;
  const queryClient = useQueryClient();
  const [sheet, setSheet] = useState<{ allocation?: PlanningAllocation; preset?: AllocationPreset }>();

  const refused = (error: Error) => {
    const code = refusalCode(error);
    notifications.show({
      color: "red",
      title: t("couldNotSave"),
      message: code && ruleRefusals.includes(code) ? t(`refusal.${code}`) : error.message,
    });
  };
  const confirm = useMutation({
    mutationFn: (allocation: PlanningAllocation) => confirmAllocation(allocation.id),
    onSuccess: async (answer) => {
      notifications.show({
        color: answer.warnings.length > 0 ? "yellow" : "teal",
        title: t("allocationConfirmed"),
        message: answer.warnings
          .map((warning) => t(`warning_${warning.code}`, { weeks: (warning.weeks ?? []).map(format.week).join(", ") }))
          .join(" "),
      });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
    onError: refused,
  });
  const remove = useMutation({
    mutationFn: (allocation: PlanningAllocation) => deleteAllocation(allocation.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("allocationDeleted"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
    onError: refused,
  });
  const askDelete = (allocation: PlanningAllocation) =>
    modals.openConfirmModal({
      title: t("deleteAllocationTitle"),
      children: <Text size="sm">{t("deleteAllocationExplained")}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(allocation),
    });

  const project = plan.data?.project;
  const name = project ? `${project.code} ${project.name}` : t("projectById", { id });
  const weeks = plan.data?.weeks.map((week) => week.monday) ?? [];
  const thisWeek = mondayOf(today());

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("navBoard"), to: BOARD_ROUTE_PATH }, { label: name }]}
        title={
          <Group gap="sm">
            <span>{name}</span>
            {project && (
              <Badge variant="light" color={statusColor[project.status]}>
                {t(`projectStatus_${project.status}`)}
              </Badge>
            )}
          </Group>
        }
        description={
          project
            ? [
                project.customer?.name,
                project.startDate || project.endDate
                  ? t("projectDates", {
                      from: project.startDate ? format.date(project.startDate) : "…",
                      to: project.endDate ? format.date(project.endDate) : "…",
                    })
                  : undefined,
              ]
                .filter(Boolean)
                .join(" · ")
            : undefined
        }
        actions={
          canManage && project ? (
            <Button
              leftSection={<IconPlus size={16} />}
              onClick={() =>
                setSheet({
                  preset: {
                    project: { id: project.id, label: name, status: project.status },
                    fromDate: window.from,
                  },
                })
              }
            >
              {t("newAllocation")}
            </Button>
          ) : undefined
        }
      />

      {plan.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPlan")}>
          {plan.error.message}
        </Alert>
      )}
      {plan.isPending && <ContentSkeleton rows={4} rowHeight={44} />}

      {plan.data && (
        <>
          <Card withBorder padding="sm" radius="md">
            <Title order={2} size="h4" mb="sm">
              {t("weeklyHours")}
            </Title>
            <Table.ScrollContainer minWidth={160 + weeks.length * WEEK_WIDTH}>
              <Table aria-label={t("weeklyHours")} layout="fixed" verticalSpacing={4}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th w={160} />
                    {weeks.map((monday) => (
                      <Table.Th key={monday} w={WEEK_WIDTH} scope="col">
                        <WeekHeader monday={monday} current={monday === thisWeek} />
                      </Table.Th>
                    ))}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  <Table.Tr>
                    <Table.Th scope="row">{t("bookedHours")}</Table.Th>
                    {plan.data.weeks.map((week) => (
                      <Table.Td key={week.monday}>{format.hours(week.booked)}</Table.Td>
                    ))}
                  </Table.Tr>
                  <Table.Tr>
                    <Table.Th scope="row">{t("tentativeHours")}</Table.Th>
                    {plan.data.weeks.map((week) => (
                      <Table.Td key={week.monday}>{format.hours(week.tentative)}</Table.Td>
                    ))}
                  </Table.Tr>
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
            {plan.data.allocations.length > 0 && (
              <div style={{ marginTop: 12, minWidth: 0, overflowX: "auto" }}>
                <div style={{ minWidth: weeks.length * WEEK_WIDTH }}>
                  <AllocationBars
                    allocations={plan.data.allocations}
                    weeks={weeks}
                    label={t("allocationsOf", { name })}
                    name={(allocation) => format.personName(allocation.person)}
                    onOpen={canManage ? (allocation) => setSheet({ allocation }) : undefined}
                  />
                </div>
              </div>
            )}
          </Card>

          <Card withBorder padding="sm" radius="md">
            <Title order={2} size="h4" mb="sm">
              {t("allocations")}
            </Title>
            {plan.data.allocations.length === 0 ? (
              <EmptyState size="sm" title={t("noAllocationsInWindow")} description={t("noAllocationsHint")} />
            ) : (
              <Table.ScrollContainer minWidth={640}>
                <Table aria-label={t("allocations")} verticalSpacing="xs" highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("person")}</Table.Th>
                      <Table.Th>{t("allocationTitle")}</Table.Th>
                      <Table.Th>{t("period")}</Table.Th>
                      <Table.Th>{t("load")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      {canManage && <Table.Th w={48}>{t("rowActions")}</Table.Th>}
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {plan.data.allocations.map((allocation) => {
                      const person = format.personName(allocation.person);
                      return (
                        <Table.Tr key={allocation.id}>
                          <Table.Td>
                            <Text size="sm" fw={500}>
                              {person}
                            </Text>
                            {allocation.note && (
                              <Text size="xs" c="dimmed">
                                {allocation.note}
                              </Text>
                            )}
                          </Table.Td>
                          <Table.Td>{allocation.title ?? ""}</Table.Td>
                          <Table.Td>{format.range(allocation.fromDate, allocation.toDate)}</Table.Td>
                          <Table.Td>{format.load(allocation.load, allocation.loadUnit)}</Table.Td>
                          <Table.Td>
                            <Badge
                              variant="light"
                              color={allocation.status === "confirmed" ? "blue" : "violet"}
                              size="sm"
                            >
                              {t(`status_${allocation.status}`)}
                            </Badge>
                          </Table.Td>
                          {canManage && (
                            <Table.Td>
                              <Menu position="bottom-end" withinPortal>
                                <Menu.Target>
                                  <ActionIcon variant="subtle" aria-label={t("actionsFor", { name: person })}>
                                    <IconDots size={16} />
                                  </ActionIcon>
                                </Menu.Target>
                                <Menu.Dropdown>
                                  <Menu.Item onClick={() => setSheet({ allocation })}>{t("edit")}</Menu.Item>
                                  {allocation.status === "tentative" && (
                                    <Menu.Item onClick={() => confirm.mutate(allocation)}>{t("confirm")}</Menu.Item>
                                  )}
                                  <Menu.Item color="red" onClick={() => askDelete(allocation)}>
                                    {t("delete")}
                                  </Menu.Item>
                                </Menu.Dropdown>
                              </Menu>
                            </Table.Td>
                          )}
                        </Table.Tr>
                      );
                    })}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </Card>
        </>
      )}

      {canManage && (
        <AllocationSheet
          opened={sheet !== undefined}
          onClose={() => setSheet(undefined)}
          allocation={sheet?.allocation}
          preset={sheet?.preset}
        />
      )}
    </Stack>
  );
};
