import { ActionIcon, Alert, Badge, Button, Card, Group, Menu, Stack, Table, Text, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDots, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import { useState } from "react";
import { confirmAllocation, deleteAllocation, type PlanningAllocation } from "../api/allocations";
import { planningMetaQueryOptions } from "../api/meta";
import { projectPlanQueryOptions } from "../api/plan";
import { PLANNING_QUERY_KEY, refusalCode, ruleRefusals } from "../api/request";
import { requestStatusColor, usePlanningFormat } from "../lib/format";
import { mondayOf, today } from "../lib/weeks";
import { planWindow } from "../lib/window";
import { AllocationBars, SpanLane } from "./allocation-bars";
import { type AllocationPreset, AllocationSheet } from "./allocation-sheet";
import { CandidatesPanel } from "./candidates-panel";
import { type RequestPreset, RequestSheet } from "./request-sheet";
import { WeekHeader } from "./week-header";
import { useWithdrawRequest } from "./withdraw-request";

export interface ProjectPlanProps {
  projectId: number;
  /** The window; left out, this week and the eleven after it (S11). */
  from?: string;
  to?: string;
}

const WEEK_WIDTH = 80;

/**
 * One project's plan (design S11, §7): the hours booked, tentative and asked
 * for each week, its allocations and its open requests as bars, the
 * allocations as a list a planner works through — change, confirm, delete —
 * and the requests with their candidates; *New allocation* and *New
 * request*. It takes nothing from the route, so the host can put it on the
 * project page's Planning tab as well as on this app's own plan page.
 * `planning:view`'s; the writes are `planning:manage`'s.
 */
export const ProjectPlan = ({ projectId, from, to }: ProjectPlanProps) => {
  const format = usePlanningFormat();
  const { t } = format;
  const window = planWindow({ from, to });
  const plan = useQuery(projectPlanQueryOptions(projectId, window));
  const meta = useQuery(planningMetaQueryOptions());
  const canManage = meta.data?.canManage === true;
  const queryClient = useQueryClient();
  const [sheet, setSheet] = useState<{ allocation?: PlanningAllocation; preset?: AllocationPreset }>();
  // A request by its id alone: the sheet and the panel read it afresh.
  const [requestSheet, setRequestSheet] = useState<{ requestId?: number; preset?: RequestPreset }>();
  const [candidatesOf, setCandidatesOf] = useState<number>();
  const withdraw = useWithdrawRequest();

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

  if (plan.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPlan")}>
        {plan.error.message}
      </Alert>
    );
  if (plan.isPending) return <ContentSkeleton rows={4} rowHeight={44} />;

  const { project, allocations, requests } = plan.data;
  const name = `${project.code} ${project.name}`;
  const picked = { id: project.id, label: name, status: project.status };
  const weeks = plan.data.weeks.map((week) => week.monday);
  const thisWeek = mondayOf(today());
  const openRequests = requests.filter((request) => request.status === "open");

  return (
    <Stack gap="lg">
      {canManage && (
        <Group justify="flex-end" gap="sm">
          <Button
            variant="default"
            leftSection={<IconPlus size={16} />}
            onClick={() => setRequestSheet({ preset: { project: picked, fromDate: window.from, toDate: window.to } })}
          >
            {t("newRequest")}
          </Button>
          <Button
            leftSection={<IconPlus size={16} />}
            onClick={() => setSheet({ preset: { project: picked, fromDate: window.from } })}
          >
            {t("newAllocation")}
          </Button>
        </Group>
      )}

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
              <Table.Tr>
                <Table.Th scope="row">{t("demandHours")}</Table.Th>
                {plan.data.weeks.map((week) => (
                  <Table.Td key={week.monday} c={week.demand > 0 ? "orange" : undefined}>
                    {format.hours(week.demand)}
                  </Table.Td>
                ))}
              </Table.Tr>
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {(allocations.length > 0 || openRequests.length > 0) && (
          <div style={{ marginTop: 12, minWidth: 0, overflowX: "auto" }}>
            <Stack gap={6} style={{ minWidth: weeks.length * WEEK_WIDTH }}>
              {allocations.length > 0 && (
                <AllocationBars
                  allocations={allocations}
                  weeks={weeks}
                  label={t("allocationsOf", { name })}
                  name={(allocation) => format.personName(allocation.person)}
                  onOpen={canManage ? (allocation) => setSheet({ allocation }) : undefined}
                />
              )}
              {openRequests.length > 0 && (
                <SpanLane
                  label={t("requestsOf", { name })}
                  weeks={weeks}
                  spans={openRequests.map((request) => ({
                    key: String(request.id),
                    span: { from: request.fromDate, to: request.toDate },
                    color: "var(--mantine-color-orange-light)",
                    title: t("requestSpan", {
                      title: request.title,
                      load: format.load(request.hoursPerWeek, "hours_per_week"),
                    }),
                    content: (
                      <Text size="xs" c="orange" truncate>
                        {request.title}
                      </Text>
                    ),
                  }))}
                />
              )}
            </Stack>
          </div>
        )}
      </Card>

      <Card withBorder padding="sm" radius="md">
        <Title order={2} size="h4" mb="sm">
          {t("allocations")}
        </Title>
        {allocations.length === 0 ? (
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
                {allocations.map((allocation) => {
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
                        <Badge variant="light" color={allocation.status === "confirmed" ? "blue" : "violet"} size="sm">
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

      <Card withBorder padding="sm" radius="md">
        <Title order={2} size="h4" mb="sm">
          {t("requests")}
        </Title>
        {requests.length === 0 ? (
          <EmptyState size="sm" title={t("noRequestsInWindow")} description={t("noRequestsDescription")} />
        ) : (
          <Table.ScrollContainer minWidth={640}>
            <Table aria-label={t("requests")} verticalSpacing="xs" highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("requestTitle")}</Table.Th>
                  <Table.Th>{t("period")}</Table.Th>
                  <Table.Th>{t("loadHoursField")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  {canManage && <Table.Th>{t("rowActions")}</Table.Th>}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {requests.map((request) => (
                  <Table.Tr key={request.id}>
                    <Table.Td>
                      <Text size="sm" fw={500}>
                        {request.title}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {request.requirements.length > 0
                          ? request.requirements.map(format.requirement).join(", ")
                          : t("noRequirements")}
                      </Text>
                      {request.note && (
                        <Text size="xs" c="dimmed">
                          {request.note}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>{format.range(request.fromDate, request.toDate)}</Table.Td>
                    <Table.Td>{format.load(request.hoursPerWeek, "hours_per_week")}</Table.Td>
                    <Table.Td>
                      <Badge variant="light" size="sm" color={requestStatusColor[request.status]}>
                        {t(`requestStatus_${request.status}`)}
                      </Badge>
                      {request.filledBy && (
                        <Text size="xs" c="dimmed">
                          {t("filledBy", { name: format.personName(request.filledBy.person) })}
                        </Text>
                      )}
                    </Table.Td>
                    {canManage && (
                      <Table.Td>
                        <Group gap={4} wrap="nowrap">
                          <Button
                            size="xs"
                            variant="light"
                            aria-label={t("candidatesFor", { title: request.title })}
                            onClick={() => setCandidatesOf(request.id)}
                          >
                            {t("candidates")}
                          </Button>
                          <Menu position="bottom-end" withinPortal>
                            <Menu.Target>
                              <ActionIcon variant="subtle" aria-label={t("actionsFor", { name: request.title })}>
                                <IconDots size={16} />
                              </ActionIcon>
                            </Menu.Target>
                            <Menu.Dropdown>
                              <Menu.Item onClick={() => setRequestSheet({ requestId: request.id })}>
                                {request.status === "open" ? t("edit") : t("openRequest")}
                              </Menu.Item>
                              {request.status === "open" && (
                                <Menu.Item color="red" onClick={() => withdraw(request)}>
                                  {t("withdraw")}
                                </Menu.Item>
                              )}
                            </Menu.Dropdown>
                          </Menu>
                        </Group>
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Card>

      {canManage && (
        <>
          <AllocationSheet
            opened={sheet !== undefined}
            onClose={() => setSheet(undefined)}
            allocation={sheet?.allocation}
            preset={sheet?.preset}
          />
          <RequestSheet
            opened={requestSheet !== undefined}
            onClose={() => setRequestSheet(undefined)}
            requestId={requestSheet?.requestId}
            preset={requestSheet?.preset}
          />
          <CandidatesPanel
            opened={candidatesOf !== undefined}
            onClose={() => setCandidatesOf(undefined)}
            requestId={candidatesOf}
          />
        </>
      )}
    </Stack>
  );
};
