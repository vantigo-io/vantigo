import { Alert, Badge, Button, Card, Drawer, em, Group, SegmentedControl, Stack, Switch, Text } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import { useState } from "react";
import { allocationStatuses, type PlanningAllocationStatus } from "../api/allocations";
import { PLANNING_QUERY_KEY, refusalCode, ruleRefusals } from "../api/request";
import {
  candidatesQueryOptions,
  fillRequest,
  type PlanningCandidate,
  type PlanningRequest,
  type PlanningRequestFilled,
  requestQueryOptions,
} from "../api/requests";
import { usePlanningFormat } from "../lib/format";
import { WarningList } from "./request-sheet";

export interface CandidatesPanelProps {
  opened: boolean;
  onClose: () => void;
  /**
   * The request whose candidates are shown, by id: the panel reads it afresh,
   * so a fill refused as request_not_open, which reloads it, takes the Fill
   * buttons away. The panel shows nothing without one.
   */
  requestId?: number;
}

/**
 * A request's candidates (design S17, §7): the people who could fill it, as
 * the server sorts them — those with the time first, then the fit to what
 * they want, the free hours, and the longest on their current project — each
 * with the figures, the flags and the competences they miss, so the planner
 * sees why. *Show partial matches* lists those who miss a requirement too;
 * *Fill* books the person on the request, tentative or confirmed. A drawer
 * at the side, a bottom sheet on a phone. `planning:manage`'s.
 */
export const CandidatesPanel = ({ opened, onClose, requestId }: CandidatesPanelProps) => {
  const { t } = usePlanningFormat();
  const phone = useMediaQuery(`(max-width: ${em(767.98)})`);
  const shown = opened && requestId !== undefined;
  const stored = useQuery({ ...requestQueryOptions(requestId ?? 0), enabled: shown });
  const request = shown ? stored.data : undefined;
  return (
    <Drawer
      opened={shown}
      onClose={onClose}
      title={request ? t("candidatesTitle", { title: request.title }) : t("candidates")}
      position={phone ? "bottom" : "right"}
      size={phone ? "90%" : "lg"}
      closeButtonProps={{ "aria-label": t("close") }}
    >
      {shown &&
        (request ? (
          <Candidates key={request.id} request={request} onDone={onClose} />
        ) : stored.isError ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadRequest")}>
            {stored.error.message}
          </Alert>
        ) : (
          <ContentSkeleton rows={3} rowHeight={72} />
        ))}
    </Drawer>
  );
};

/** A fill starts tentative on a planned project and confirmed on any other, as an allocation does (S3). */
const statusFor = (request: PlanningRequest): PlanningAllocationStatus =>
  request.project.status === "planned" ? "tentative" : "confirmed";

const Candidates = ({ request, onDone }: { request: PlanningRequest; onDone: () => void }) => {
  const format = usePlanningFormat();
  const { t } = format;
  const queryClient = useQueryClient();
  const [includePartial, setIncludePartial] = useState(false);
  const [status, setStatus] = useState<PlanningAllocationStatus>(statusFor(request));
  const [filled, setFilled] = useState<PlanningRequestFilled>();
  const [refusal, setRefusal] = useState<string>();
  const candidates = useQuery(candidatesQueryOptions(request.id, includePartial));
  const open = request.status === "open";

  const fill = useMutation({
    mutationFn: (candidate: PlanningCandidate) => fillRequest(request.id, { personId: candidate.person.id, status }),
    onMutate: () => setRefusal(undefined),
    onSuccess: async (answer) => {
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
      if (answer.warnings.length > 0) {
        setFilled(answer);
        return;
      }
      notifications.show({ color: "teal", title: t("requestFilled"), message: "" });
      onDone();
    },
    onError: async (error: Error) => {
      const code = refusalCode(error);
      setRefusal(code && ruleRefusals.includes(code) ? t(`refusal.${code}`) : error.message);
      if (code === "request_not_open") await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
  });

  if (filled)
    return (
      <Stack>
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("filledWithWarnings")}>
          <WarningList warnings={filled.warnings} week={format.week} />
        </Alert>
        <Group justify="flex-end">
          <Button onClick={onDone}>{t("close")}</Button>
        </Group>
      </Stack>
    );

  /** A requirement's name by its competence key, as the request names it. */
  const nameOfKey = (key: string) => {
    const requirement = request.requirements.find((one) => one.key === key);
    return requirement ? format.competenceName(requirement) : key;
  };

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        {[
          format.projectName(request.project),
          format.range(request.fromDate, request.toDate),
          format.load(request.hoursPerWeek, "hours_per_week"),
        ].join(" · ")}
      </Text>
      {!open && (
        <Alert color="blue" icon={<IconInfoCircle size={16} />}>
          {t("candidatesNotOpen")}
        </Alert>
      )}
      <Group justify="space-between" align="end" gap="sm">
        <Switch
          label={t("showPartialMatches")}
          description={t("showPartialMatchesDescription")}
          checked={includePartial}
          onChange={(event) => setIncludePartial(event.currentTarget.checked)}
        />
        {open && (
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              {t("fillAs")}
            </Text>
            <SegmentedControl
              aria-label={t("fillAs")}
              size="xs"
              value={status}
              onChange={(value) => setStatus(value as PlanningAllocationStatus)}
              data={allocationStatuses.map((value) => ({ value, label: t(`status_${value}`) }))}
            />
          </Stack>
        )}
      </Group>
      {refusal && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotFill")}>
          {refusal}
        </Alert>
      )}
      {candidates.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadCandidates")}>
          {(candidates.error as { status?: number }).status === 400
            ? t("refusal.window_out_of_bounds")
            : candidates.error.message}
        </Alert>
      )}
      {candidates.isPending && <ContentSkeleton rows={3} rowHeight={72} />}
      {candidates.data && candidates.data.candidates.length === 0 && (
        <EmptyState
          size="sm"
          title={t("noCandidates")}
          description={includePartial ? undefined : t("noCandidatesHint")}
        />
      )}
      {candidates.data && candidates.data.candidates.length > 0 && (
        <Stack component="ul" gap="xs" m={0} p={0} style={{ listStyle: "none" }} aria-label={t("candidates")}>
          {candidates.data.candidates.map((candidate) => {
            const name = candidate.person.displayName;
            return (
              <Card component="li" key={candidate.person.id} withBorder padding="sm" radius="md" aria-label={name}>
                <Group justify="space-between" align="flex-start" wrap="nowrap" gap="sm">
                  <Stack gap={4} style={{ minWidth: 0 }}>
                    <Text size="sm" fw={500}>
                      {name}
                      <Text span size="xs" c="dimmed" ml={6}>
                        {[candidate.person.employeeNumber, candidate.person.department?.name]
                          .filter(Boolean)
                          .join(" · ")}
                      </Text>
                    </Text>
                    <Group gap={4}>
                      <Badge variant="light" size="sm" color={candidate.availability === "fits" ? "teal" : "yellow"}>
                        {t(`availability_${candidate.availability}`)}
                      </Badge>
                      <Badge variant="light" size="sm" color={candidate.fit.score >= 80 ? "blue" : "gray"}>
                        {t("fitScore", { score: candidate.fit.score })}
                      </Badge>
                      {candidate.covers && (
                        <Badge variant="filled" size="sm" color="teal">
                          {t("coversRequest")}
                        </Badge>
                      )}
                      {candidate.flags.map((flag) => (
                        <Badge
                          key={`${flag.code}/${flag.competenceKey ?? ""}/${flag.date ?? ""}`}
                          variant="light"
                          size="sm"
                          color="orange"
                        >
                          {t(`candidateFlag_${flag.code}`, {
                            date: flag.date ? format.date(flag.date) : "",
                            competence: flag.competenceKey ? nameOfKey(flag.competenceKey) : "",
                          })}
                        </Badge>
                      ))}
                    </Group>
                    <Text size="xs">
                      {t("freeOfRequired", {
                        free: format.hours(candidate.freeHours),
                        required: format.hours(candidate.requiredHours),
                      })}
                    </Text>
                    {candidate.missing.length > 0 && (
                      <Text size="xs" c="red">
                        {t("missingCompetences", { keys: candidate.missing.map(nameOfKey).join(", ") })}
                      </Text>
                    )}
                    {candidate.fit.lowered.length > 0 && (
                      <Text size="xs" c="dimmed">
                        {t("fitLowered", {
                          kinds: candidate.fit.lowered
                            .map((kind) => t(`preferenceKind_${kind}`, { defaultValue: kind }))
                            .join(", "),
                        })}
                      </Text>
                    )}
                    {candidate.currentProject && (
                      <Text size="xs" c="dimmed">
                        {t("currentProject", {
                          project: format.projectName(candidate.currentProject.project),
                          days: candidate.currentProject.days,
                        })}
                      </Text>
                    )}
                  </Stack>
                  {open && (
                    <Button
                      size="xs"
                      variant={candidate.covers ? "filled" : "light"}
                      aria-label={t("fillWith", { name })}
                      loading={fill.isPending && fill.variables?.person.id === candidate.person.id}
                      disabled={fill.isPending}
                      onClick={() => fill.mutate(candidate)}
                    >
                      {t("fill")}
                    </Button>
                  )}
                </Group>
              </Card>
            );
          })}
        </Stack>
      )}
    </Stack>
  );
};
