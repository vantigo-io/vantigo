import { Alert, Anchor, Badge, Button, Card, Group, SimpleGrid, Stack, Table, Text, Title } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { intakeQueryOptions, type PlanningCoverage } from "../api/intake";
import { planningMetaQueryOptions } from "../api/meta";
import { CandidatesPanel } from "../components/candidates-panel";
import { usePlanningFormat } from "../lib/format";
import { projectPlanLinkOptions } from "../lib/routes";

/** A coverage's badge colour. */
const coverageColor: Record<PlanningCoverage, string> = {
  covered: "teal",
  partly_covered: "yellow",
  uncovered: "red",
};

/**
 * The intake (design S18, §7): the pipeline — every planned project with its
 * hours a week requested, tentative and confirmed, and each request covered,
 * partly covered or uncovered by the people who could fill it, with the
 * key-person badge when exactly one could. `planning:view`'s; a request's
 * candidates are `planning:manage`'s.
 */
export const IntakePage = () => {
  const format = usePlanningFormat();
  const { t } = format;
  const intake = useQuery(intakeQueryOptions());
  const meta = useQuery(planningMetaQueryOptions());
  const canManage = meta.data?.canManage === true;
  const [candidatesOf, setCandidatesOf] = useState<number>();
  const perWeek = (value: number) => format.load(value, "hours_per_week");

  return (
    <Stack gap="lg">
      <PageHeader title={t("intakeTitle")} description={t("intakeDescription")} />
      {intake.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadIntake")}>
          {intake.error.message}
        </Alert>
      )}
      {intake.isPending && <ContentSkeleton rows={4} rowHeight={56} />}
      {intake.data && intake.data.projects.length === 0 && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState title={t("noPlannedProjects")} description={t("noPlannedProjectsDescription")} />
        </Card>
      )}
      {intake.data?.projects.map((entry) => {
        const name = `${entry.project.code} ${entry.project.name}`;
        return (
          <Card key={entry.project.id} withBorder padding="sm" radius="md" component="section" aria-label={name}>
            <Stack gap="sm">
              <div>
                <Title order={2} size="h4">
                  <Anchor
                    inherit
                    renderRoot={(props) => <Link {...projectPlanLinkOptions(entry.project.id)} {...props} />}
                  >
                    {name}
                  </Anchor>
                </Title>
                <Text size="sm" c="dimmed">
                  {[
                    entry.project.customer?.name,
                    entry.project.startDate || entry.project.endDate
                      ? t("projectDates", {
                          from: entry.project.startDate ? format.date(entry.project.startDate) : "…",
                          to: entry.project.endDate ? format.date(entry.project.endDate) : "…",
                        })
                      : undefined,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </Text>
              </div>
              <SimpleGrid cols={3} spacing="xs">
                <Figure label={t("requestedHours")} value={perWeek(entry.requestedHoursPerWeek)} />
                <Figure label={t("tentativeHours")} value={perWeek(entry.tentativeHoursPerWeek)} />
                <Figure label={t("bookedHours")} value={perWeek(entry.confirmedHoursPerWeek)} />
              </SimpleGrid>
              {entry.requests.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("noRequestsYet")}
                </Text>
              ) : (
                <Table.ScrollContainer minWidth={640}>
                  <Table aria-label={t("requestsOf", { name })} verticalSpacing="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("requestTitle")}</Table.Th>
                        <Table.Th>{t("period")}</Table.Th>
                        <Table.Th>{t("loadHoursField")}</Table.Th>
                        <Table.Th>{t("coverage")}</Table.Th>
                        {canManage && <Table.Th>{t("rowActions")}</Table.Th>}
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {entry.requests.map(({ request, coverage, candidates, flags }) => (
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
                            {request.filledBy && (
                              <Text size="xs" c="dimmed">
                                {t("filledBy", { name: format.personName(request.filledBy.person) })}
                              </Text>
                            )}
                          </Table.Td>
                          <Table.Td>{format.range(request.fromDate, request.toDate)}</Table.Td>
                          <Table.Td>{perWeek(request.hoursPerWeek)}</Table.Td>
                          <Table.Td>
                            <Group gap={4}>
                              <Badge variant="light" size="sm" color={coverageColor[coverage]}>
                                {t(`coverage_${coverage}`)}
                              </Badge>
                              {flags.map((flag) => (
                                <Badge key={flag} variant="light" size="sm" color="orange">
                                  {t(`intakeFlag_${flag}`)}
                                </Badge>
                              ))}
                            </Group>
                            {request.status === "open" && (
                              <Text size="xs" c="dimmed">
                                {t("coveringCandidates", { number: candidates })}
                              </Text>
                            )}
                          </Table.Td>
                          {canManage && (
                            <Table.Td>
                              <Button
                                size="xs"
                                variant="light"
                                aria-label={t("candidatesFor", { title: request.title })}
                                onClick={() => setCandidatesOf(request.id)}
                              >
                                {t("candidates")}
                              </Button>
                            </Table.Td>
                          )}
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              )}
            </Stack>
          </Card>
        );
      })}
      {canManage && (
        <CandidatesPanel
          opened={candidatesOf !== undefined}
          onClose={() => setCandidatesOf(undefined)}
          requestId={candidatesOf}
        />
      )}
    </Stack>
  );
};

const Figure = ({ label, value }: { label: string; value: string }) => (
  <div>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm" fw={600}>
      {value}
    </Text>
  </div>
);
