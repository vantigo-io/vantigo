import { Button, Card, Group, Stack, Text, Textarea } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconCheck, IconX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useLocale } from "@vantigo/frontend-shell";
import { useState } from "react";
import { approvalsQueryOptions, approveAbsence, rejectAbsence, type StaffApproval } from "../api/absence";
import type { StaffAbsenceType } from "../api/meta";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { absenceDays, absenceName, refusalText, useAbsenceDetails } from "../lib/absence";
import { useStaffFormat } from "../lib/format";
import { FieldGroup } from "./field-group";

/*
 * The approvals inbox (design §7): what `GET /approvals` answers for the
 * caller and nothing else — the server decides whose requests reach whom
 * (an HR-only type's only staff:personal-data's, plan reading 33) — each
 * approved or rejected, with its reason, where it stands. My team and the
 * Absence page both show it.
 */

/** The requests I decide, oldest first, each approved or rejected — with its reason — where it stands. */
export const ApprovalsInbox = ({ types }: { types: readonly StaffAbsenceType[] }) => {
  const { t } = useStaffFormat();
  const approvals = useQuery(approvalsQueryOptions());
  return (
    <FieldGroup title={t("approvalsTitle")} description={t("approvalsDescription")}>
      {approvals.isPending && <ContentSkeleton rows={2} rowHeight={44} />}
      {approvals.data?.data.length === 0 && <Text size="sm">{t("noApprovals")}</Text>}
      {approvals.data?.data.map((approval) => (
        <ApprovalRow key={approval.absence.id} approval={approval} types={types} />
      ))}
    </FieldGroup>
  );
};

const ApprovalRow = ({ approval, types }: { approval: StaffApproval; types: readonly StaffAbsenceType[] }) => {
  const { t, date } = useStaffFormat();
  const locale = useLocale();
  const queryClient = useQueryClient();
  const details = useAbsenceDetails();
  const [rejecting, setRejecting] = useState(false);
  const { absence, person } = approval;
  const name = absenceName(absence, types, locale, t);
  const label = t("approvalLabel", {
    person: person.displayName,
    absence: t("absenceLabel", { name, days: absenceDays(absence, date) }),
  });
  const form = useForm({
    initialValues: { reason: "" },
    validate: { reason: (value) => (value.trim() ? null : t("rejectReasonRequired")) },
  });
  const decided = async (title: string) => {
    notifications.show({ color: "teal", title, message: "" });
    await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
  };
  const failed = async (error: Error) => {
    // An overlap found at approval is the person's, not the decider's own.
    const message =
      (error as { code?: unknown }).code === "absence_overlap"
        ? t("refusal.absence_overlap_decide")
        : refusalText(error, t);
    notifications.show({ color: "red", title: t("couldNotDecide"), message });
    // Somebody quicker, or a withdrawn request: the inbox is read again.
    await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "approvals"] });
  };
  const approve = useMutation({
    mutationFn: () => approveAbsence(absence.id),
    onSuccess: () => decided(t("requestApproved")),
    onError: failed,
  });
  const reject = useMutation({
    mutationFn: (reason: string) => rejectAbsence(absence.id, reason),
    onSuccess: () => decided(t("requestRejected")),
    onError: failed,
  });

  return (
    <Card withBorder radius="md" padding="sm" component="article" aria-label={label}>
      <Stack gap={4}>
        <Text fw={500}>{person.displayName}</Text>
        <Text size="sm">{`${name} · ${absenceDays(absence, date)}`}</Text>
        {details(absence).map((detail) => (
          <Text key={detail} size="sm" c="dimmed">
            {detail}
          </Text>
        ))}
        {absence.note && <Text size="sm">{absence.note}</Text>}
        {rejecting ? (
          <form onSubmit={form.onSubmit((values) => reject.mutate(values.reason.trim()))}>
            <Stack gap="xs">
              <Textarea
                label={t("rejectReason")}
                description={t("rejectReasonDescription")}
                withAsterisk
                rows={3}
                maxLength={500}
                {...form.getInputProps("reason")}
              />
              <Group gap="xs">
                <Button type="submit" size="xs" color="red" loading={reject.isPending}>
                  {t("rejectRequest")}
                </Button>
                <Button type="button" size="xs" variant="default" onClick={() => setRejecting(false)}>
                  {t("cancel")}
                </Button>
              </Group>
            </Stack>
          </form>
        ) : (
          <Group gap="xs">
            <Button
              size="xs"
              leftSection={<IconCheck size={14} />}
              loading={approve.isPending}
              onClick={() => approve.mutate()}
            >
              {t("approve")}
            </Button>
            <Button
              size="xs"
              variant="light"
              color="red"
              leftSection={<IconX size={14} />}
              onClick={() => setRejecting(true)}
            >
              {t("reject")}
            </Button>
          </Group>
        )}
      </Stack>
    </Card>
  );
};
