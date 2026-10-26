import { Alert, Anchor, Badge, Button, Card, Group, Select, Stack, Table, Text, UnstyledButton } from "@mantine/core";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { planningMetaQueryOptions } from "../api/meta";
import { REQUESTS_LIMIT, requestStatuses, requestsQueryOptions } from "../api/requests";
import { CandidatesPanel } from "../components/candidates-panel";
import { ProjectPicker } from "../components/project-picker";
import { RequestSheet } from "../components/request-sheet";
import { useWithdrawRequest } from "../components/withdraw-request";
import { requestStatusColor, usePlanningFormat } from "../lib/format";
import { projectPlanLinkOptions } from "../lib/routes";
import type { RequestsSearch, RequestsStatusFilter } from "../lib/search";

/**
 * The requests (design S16, §7): what the projects need before anybody is
 * named — open, filled and withdrawn — filtered by status, the open ones
 * unless another is picked, and by project. A
 * planner opens a request to change it, its candidates to fill it, or
 * withdraws it; *New request* files one. `planning:view`'s; the writes and
 * the candidates are `planning:manage`'s.
 */
export const RequestsPage = () => {
  const format = usePlanningFormat();
  const { t } = format;
  const search = useSearch({ strict: false }) as RequestsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const meta = useQuery(planningMetaQueryOptions());
  const canManage = meta.data?.canManage === true;
  const status: RequestsStatusFilter = search.status ?? "open";
  const requests = useQuery(
    requestsQueryOptions({ status: status === "all" ? undefined : status, projectId: search.projectId }),
  );
  // The ids alone: the sheet and the panel read the request afresh, so a
  // refusal that reloads it (request_not_open) shows what it is now.
  const [sheet, setSheet] = useState<{ requestId?: number }>();
  const [candidatesOf, setCandidatesOf] = useState<number>();
  const withdraw = useWithdrawRequest();
  const creating = search.new === 1 && canManage;

  const setSearch = (patch: Partial<RequestsSearch>) =>
    navigate({ search: (previous: RequestsSearch) => ({ ...previous, ...patch }), replace: true });
  const closeSheet = () => {
    setSheet(undefined);
    if (search.new) setSearch({ new: undefined });
  };

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("requestsTitle")}
        description={t("requestsDescription")}
        actions={
          canManage ? (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setSheet({})}>
              {t("newRequest")}
            </Button>
          ) : undefined
        }
      />

      <Group align="end" gap="sm" wrap="wrap">
        <Select
          label={t("status")}
          w={200}
          allowDeselect={false}
          data={[
            { value: "all", label: t("allStatuses") },
            ...requestStatuses.map((value) => ({ value, label: t(`requestStatus_${value}`) })),
          ]}
          value={status}
          onChange={(value) => value && setSearch({ status: value as RequestsStatusFilter })}
        />
        <div style={{ width: 260, maxWidth: "100%" }}>
          <ProjectPicker
            label={t("project")}
            placeholder={t("allProjects")}
            clearable
            value={search.projectId ?? null}
            onChange={(id) => setSearch({ projectId: id ?? undefined })}
          />
        </div>
      </Group>

      <Card withBorder padding="sm" radius="md">
        {requests.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadRequests")}>
            {requests.error.message}
          </Alert>
        )}
        {requests.isPending && <ContentSkeleton rows={4} rowHeight={44} />}
        {requests.data && requests.data.length === 0 && (
          <EmptyState title={t("noRequests")} description={canManage ? t("noRequestsDescription") : undefined} />
        )}
        {requests.data && requests.data.length > 0 && (
          <Table.ScrollContainer minWidth={760}>
            <Table aria-label={t("requestsTitle")} verticalSpacing="xs" highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("requestTitle")}</Table.Th>
                  <Table.Th>{t("project")}</Table.Th>
                  <Table.Th>{t("period")}</Table.Th>
                  <Table.Th>{t("loadHoursField")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  {canManage && <Table.Th>{t("rowActions")}</Table.Th>}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {requests.data.map((request) => (
                  <Table.Tr key={request.id}>
                    <Table.Td>
                      {canManage ? (
                        <UnstyledButton
                          onClick={() => setSheet({ requestId: request.id })}
                          aria-label={t("editRequestNamed", { title: request.title })}
                        >
                          <Text size="sm" fw={500} c="blue">
                            {request.title}
                          </Text>
                        </UnstyledButton>
                      ) : (
                        <Text size="sm" fw={500}>
                          {request.title}
                        </Text>
                      )}
                      <Text size="xs" c="dimmed">
                        {request.requirements.length > 0
                          ? request.requirements.map(format.requirement).join(", ")
                          : t("noRequirements")}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Anchor
                        size="sm"
                        renderRoot={(props) => <Link {...projectPlanLinkOptions(request.project.id)} {...props} />}
                      >
                        {format.projectName(request.project)}
                      </Anchor>
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
                          {request.status === "open" && (
                            <Button
                              size="xs"
                              variant="subtle"
                              color="red"
                              aria-label={t("withdrawNamed", { title: request.title })}
                              onClick={() => withdraw(request)}
                            >
                              {t("withdraw")}
                            </Button>
                          )}
                        </Group>
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        {requests.data && requests.data.length >= REQUESTS_LIMIT && (
          <Text size="xs" c="dimmed" mt="xs">
            {t("requestsCapped", { number: REQUESTS_LIMIT })}
          </Text>
        )}
      </Card>

      {canManage && (
        <>
          <RequestSheet opened={sheet !== undefined || creating} onClose={closeSheet} requestId={sheet?.requestId} />
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
