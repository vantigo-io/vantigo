import { Alert, Badge, Button, Card, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus, IconX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useLocale } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  absenceIfMatch,
  cancelAbsence,
  myAbsencesQueryOptions,
  myCareDaysQueryOptions,
  myHolidayAccountQueryOptions,
  mySelfCertificationQueryOptions,
  type StaffAbsence,
} from "../api/absence";
import type { StaffAbsenceType } from "../api/meta";
import { STAFF_QUERY_KEY } from "../api/request";
import { CareDaysCard } from "../components/care-days-card";
import { FieldGroup } from "../components/field-group";
import { HolidayAccountCard } from "../components/holiday-account-card";
import { RequestSheet } from "../components/request-sheet";
import { SelfCertificationCard } from "../components/self-certification";
import "../i18n";
import { absenceDays, absenceName, refusalText, useAbsenceDetails } from "../lib/absence";
import { useStaffFormat } from "../lib/format";

/**
 * My page's absence (design §7): my holiday account, my egenmelding
 * standing, my care days when the server answers them, my absences — a
 * request to change or cancel while it waits — and the request sheet. Every
 * figure is the server's; the page counts nothing but the sheet's estimate.
 */
export const MyAbsence = ({ today, types }: { today: string; types: readonly StaffAbsenceType[] }) => {
  const { t } = useStaffFormat();
  const year = Number(today.slice(0, 4));
  const account = useQuery(myHolidayAccountQueryOptions(year));
  const standing = useQuery(mySelfCertificationQueryOptions());
  const careDays = useQuery(myCareDaysQueryOptions(year));
  const absences = useQuery(myAbsencesQueryOptions());
  const [sheet, setSheet] = useState<{ absence?: StaffAbsence } | undefined>();

  return (
    <Stack gap="md">
      <Group justify="flex-end">
        <Button leftSection={<IconPlus size={16} />} onClick={() => setSheet({})}>
          {t("requestAbsence")}
        </Button>
      </Group>
      {(account.isError || absences.isError) && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadAbsence")}>
          {(account.error ?? absences.error)?.message}
        </Alert>
      )}
      {account.data ? <HolidayAccountCard account={account.data} /> : account.isPending && <ContentSkeleton rows={3} />}
      {standing.data && <SelfCertificationCard standing={standing.data} />}
      {careDays.data && <CareDaysCard careDays={careDays.data} />}
      <FieldGroup title={t("myAbsences")}>
        {absences.isPending && <ContentSkeleton rows={3} rowHeight={44} />}
        {absences.data?.data.length === 0 && <EmptyState title={t("noAbsences")} />}
        {absences.data?.data.map((absence) => (
          <MyAbsenceRow key={absence.id} absence={absence} types={types} onChange={() => setSheet({ absence })} />
        ))}
      </FieldGroup>
      <RequestSheet
        opened={sheet !== undefined}
        onClose={() => setSheet(undefined)}
        types={types}
        today={today}
        absence={sheet?.absence}
      />
    </Stack>
  );
};

/**
 * One of my absences as the server shaped it: what it carries and nothing
 * more. A request I may still change carries the token its change sends.
 */
const MyAbsenceRow = ({
  absence,
  types,
  onChange,
}: {
  absence: StaffAbsence;
  types: readonly StaffAbsenceType[];
  onChange: () => void;
}) => {
  const { t, date } = useStaffFormat();
  const locale = useLocale();
  const queryClient = useQueryClient();
  const name = absenceName(absence, types, locale, t);
  const days = absenceDays(absence, date);
  const waiting = absence.status === "requested";
  // A change sends the row's own token and names its type: a row carrying
  // neither is not mine to change here.
  const changeable = absence.shownAs === "type" && absenceIfMatch(absence) !== undefined;
  const cancel = useMutation({
    mutationFn: () => cancelAbsence(absence.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("requestCancelled"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCancelRequest"), message: refusalText(error, t) }),
  });
  const confirmCancel = () =>
    modals.openConfirmModal({
      title: t("cancelRequestTitle"),
      children: <Text size="sm">{t("cancelRequestExplained")}</Text>,
      labels: { confirm: t("cancelTheRequest"), cancel: t("keepTheRequest") },
      confirmProps: { color: "red" },
      onConfirm: () => cancel.mutate(),
    });
  const describe = useAbsenceDetails();
  const details = describe(absence);

  return (
    <Card withBorder radius="md" padding="sm" component="article" aria-label={t("absenceLabel", { name, days })}>
      <Stack gap={4}>
        <Group justify="space-between" wrap="wrap" gap="xs">
          <Text fw={500}>{name}</Text>
          {absence.status && <Badge variant="light">{t(`absenceStatus_${absence.status}`)}</Badge>}
        </Group>
        <Text size="sm">{days}</Text>
        {details.map((detail) => (
          <Text key={detail} size="sm" c="dimmed">
            {detail}
          </Text>
        ))}
        {absence.note && <Text size="sm">{absence.note}</Text>}
        {absence.decisionNote && (
          <Stack gap={0}>
            <Text size="xs" c="dimmed">
              {t("decisionNote")}
            </Text>
            <Text size="sm">{absence.decisionNote}</Text>
          </Stack>
        )}
        {waiting && (
          <Group gap="xs">
            {changeable && (
              <Button size="xs" variant="light" leftSection={<IconPencil size={14} />} onClick={onChange}>
                {t("changeAbsenceButton")}
              </Button>
            )}
            <Button
              size="xs"
              variant="subtle"
              color="red"
              leftSection={<IconX size={14} />}
              loading={cancel.isPending}
              onClick={confirmCancel}
            >
              {t("cancelRequest")}
            </Button>
          </Group>
        )}
      </Stack>
    </Card>
  );
};
