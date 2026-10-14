import { Badge, Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { markMilestoneDone, type StaffCase, type StaffCaseMilestone, waiveMilestone } from "../api/cases";
import { STAFF_QUERY_KEY } from "../api/request";
import { refusalText } from "../lib/absence";
import { useStaffFormat } from "../lib/format";

/**
 * A case's follow-up milestones as the server dated and judged them (design
 * S10): each with its date, whether it is due, done or waived, and — for
 * those who may — *Mark done* and *Waive*. The end of the employer's period
 * is the rows' to reach, never ticked or waived. A note or a waiver's reason
 * shows only when the response carries it.
 */
export const MilestoneList = ({ sickCase }: { sickCase: StaffCase }) => {
  const [waiving, setWaiving] = useState<StaffCaseMilestone | undefined>();
  return (
    <Stack gap="xs">
      {sickCase.milestones.map((milestone) => (
        <MilestoneRow
          key={milestone.kind}
          sickCase={sickCase}
          milestone={milestone}
          onWaive={() => setWaiving(milestone)}
        />
      ))}
      <WaiveModal sickCase={sickCase} milestone={waiving} onClose={() => setWaiving(undefined)} />
    </Stack>
  );
};

const MilestoneRow = ({
  sickCase,
  milestone,
  onWaive,
}: {
  sickCase: StaffCase;
  milestone: StaffCaseMilestone;
  onWaive: () => void;
}) => {
  const { t, date } = useStaffFormat();
  const queryClient = useQueryClient();
  const label = t(`milestone_${milestone.kind}`);
  const done = useMutation({
    mutationFn: () => markMilestoneDone(sickCase.id, milestone.kind),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] }),
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotChangeMilestone"), message: refusalText(error, t) }),
  });
  const derived = milestone.kind === "employer_period_end";
  return (
    <Group role="group" aria-label={label} justify="space-between" wrap="wrap" gap="xs">
      <Stack gap={0}>
        <Text size="sm" fw={500}>
          {label}
        </Text>
        {milestone.dueDate && (
          <Text size="xs" c="dimmed">
            {date(milestone.dueDate)}
          </Text>
        )}
        {milestone.waivedReason && <Text size="xs">{milestone.waivedReason}</Text>}
        {milestone.note && <Text size="xs">{milestone.note}</Text>}
      </Stack>
      <Group gap="xs">
        {milestone.due && (
          <Badge color="orange" variant="light">
            {t("milestoneDue")}
          </Badge>
        )}
        {milestone.doneAt && (
          <Badge color="teal" variant="light">
            {t("milestoneDone", { date: date(milestone.doneAt) })}
          </Badge>
        )}
        {milestone.waived && <Badge variant="light">{t("milestoneWaived")}</Badge>}
        {!derived && !milestone.doneAt && (
          <Button size="xs" variant="light" loading={done.isPending} onClick={() => done.mutate()}>
            {t("markDone")}
          </Button>
        )}
        {!derived && !milestone.waived && (
          <Button size="xs" variant="subtle" onClick={onWaive}>
            {t("waive")}
          </Button>
        )}
      </Group>
    </Group>
  );
};

const WaiveModal = ({
  sickCase,
  milestone,
  onClose,
}: {
  sickCase: StaffCase;
  milestone?: StaffCaseMilestone;
  onClose: () => void;
}) => {
  const { t } = useStaffFormat();
  return (
    <Modal
      opened={milestone !== undefined}
      onClose={onClose}
      title={milestone ? t("waiveTitle", { milestone: t(`milestone_${milestone.kind}`) }) : ""}
    >
      {milestone && <WaiveForm sickCase={sickCase} milestone={milestone} onDone={onClose} />}
    </Modal>
  );
};

const WaiveForm = ({
  sickCase,
  milestone,
  onDone,
}: {
  sickCase: StaffCase;
  milestone: StaffCaseMilestone;
  onDone: () => void;
}) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const form = useForm({
    initialValues: { reason: "" },
    validate: { reason: (value) => (value.trim() ? null : t("waiveReasonRequired")) },
  });
  const waive = useMutation({
    mutationFn: (reason: string) => waiveMilestone(sickCase.id, milestone.kind, reason),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => form.setErrors({ reason: refusalText(error, t) }),
  });
  return (
    <form onSubmit={form.onSubmit((values) => waive.mutate(values.reason.trim()))}>
      <Stack>
        <Textarea
          label={t("waiveReason")}
          description={t("waiveReasonDescription")}
          withAsterisk
          rows={3}
          maxLength={500}
          {...form.getInputProps("reason")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={waive.isPending}>
            {t("waive")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
