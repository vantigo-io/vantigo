import { Button, Group, Modal, Select, Stack } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { endEmployment, type StaffEmployment, type StaffEndReason } from "../api/employments";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { conflictField, fieldRefusals } from "../lib/errors";
import { endReasons } from "../lib/terms";

export interface EndEmploymentModalProps {
  employment: StaffEmployment;
  /** The business zone's day the end starts from. */
  today: string;
  opened: boolean;
  onClose: () => void;
}

interface EndFormValues {
  endDate: string | null;
  endReason: StaffEndReason | null;
}

/**
 * *End employment* (`POST /employments/{id}/end`): the last day worked —
 * today in the business zone unless HR says otherwise, and allowed in the
 * future — and one of the a-melding's six reasons, which the form asks for
 * before anything is sent. Ending an ended employment moves its end.
 */
export const EndEmploymentModal = ({ employment, today, opened, onClose }: EndEmploymentModalProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={t("endEmploymentTitle", { number: employment.employmentNumber ?? "" })}
      fullScreen={phone}
    >
      {opened && <EndForm employment={employment} today={today} onDone={onClose} />}
    </Modal>
  );
};

const EndForm = ({ employment, today, onDone }: { employment: StaffEmployment; today: string; onDone: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const form = useForm<EndFormValues>({
    initialValues: { endDate: employment.endDate ?? today, endReason: employment.endReason ?? null },
    validate: {
      endDate: (value) => (value ? null : t("endDateRequired")),
      endReason: (value) => (value ? null : t("endReasonRequired")),
    },
  });
  const save = useMutation({
    mutationFn: (values: EndFormValues) =>
      endEmployment(employment.id, {
        endDate: values.endDate as string,
        endReason: values.endReason as StaffEndReason,
      }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("employmentEnded"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => {
      // A terms row after the end is a 409 about the day.
      const placed = conflictField(error, { terms_after_end: "endDate" });
      if (placed) return form.setErrors(placed);
      const { fields, rest } = fieldRefusals(error, ["endDate", "endReason"]);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotEndEmployment"), message: rest.join(" ") });
    },
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <DateInput
          label={t("lastDayWorked")}
          description={t("lastDayWorkedDescription")}
          withAsterisk
          valueFormat={t("dateInputFormat")}
          minDate={employment.startDate}
          {...form.getInputProps("endDate")}
        />
        <Select
          label={t("endReason")}
          description={t("endReasonDescription")}
          withAsterisk
          allowDeselect={false}
          data={endReasons.map((value) => ({ value, label: t(`endReason_${value}`) }))}
          {...form.getInputProps("endReason")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" color="orange" loading={save.isPending}>
            {t("endEmployment")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
