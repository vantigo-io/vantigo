import { Alert, Button, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconArchive, IconChecks } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { markReassessed, type StaffPerson } from "../api/people";
import { type ApiError, STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useStaffFormat } from "../lib/format";

/**
 * An erased record (design S15), for the hold group's readers — the only
 * ones `erasedAt` is answered to. It says what the erase took and what is
 * left, and why the page offers no change: the record is read-only but for
 * the legal hold, the reassessment, unlinking the user and a rehire, which
 * is Rehire on the Employments tab. The reassessment is recorded here by
 * `staff:personal-data` (`canHold`), so the dashboard's `reassessmentDue`
 * item, which opens this page, finds it at once.
 */
export const ErasedBanner = ({ person, timeZone }: { person: StaffPerson; timeZone: string }) => {
  const format = useStaffFormat();
  const t = format.t;
  const queryClient = useQueryClient();

  const reassess = useMutation({
    mutationFn: () => markReassessed(person.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("reassessmentRecorded"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "people"] });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("couldNotRecordReassessment"),
        // The server's sentence is about the rule's terms; the reader is told the rule.
        message: (error as ApiError).code === "reassessment_not_due" ? t("reassessmentNotDue") : refusalMessage(error),
      }),
  });

  const confirmReassess = () =>
    modals.openConfirmModal({
      title: t("markReassessedTitle", { name: person.displayName }),
      children: <Text size="sm">{t("markReassessedExplained")}</Text>,
      labels: { confirm: t("markReassessed"), cancel: t("cancel") },
      onConfirm: () => reassess.mutate(),
    });

  if (!person.erasedAt) return null;
  return (
    <Alert
      color="gray"
      variant="light"
      icon={<IconArchive size={16} />}
      title={t("erasedTitle", { date: format.day(person.erasedAt, timeZone) })}
    >
      <Stack gap="xs">
        <Text size="sm">{t("erasedExplained")}</Text>
        {person.reassessedAt ? (
          <Text size="sm" fw={500}>
            {t("reassessedOn", { date: format.day(person.reassessedAt, timeZone) })}
          </Text>
        ) : (
          <Group justify="space-between" gap="xs" wrap="wrap">
            <Text size="sm" c="dimmed">
              {t("reassessmentPending")}
            </Text>
            {person.capabilities.canHold && (
              <Button
                size="xs"
                variant="light"
                leftSection={<IconChecks size={14} />}
                loading={reassess.isPending}
                onClick={confirmReassess}
              >
                {t("markReassessed")}
              </Button>
            )}
          </Group>
        )}
      </Stack>
    </Alert>
  );
};
