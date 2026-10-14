import { Button, Card, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconPencil } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { closeCase, type StaffCase, updateAccommodationNote, updateMaxBenefitEstimate } from "../api/cases";
import { STAFF_QUERY_KEY } from "../api/request";
import { refusalText } from "../lib/absence";
import { isRevisionConflict } from "../lib/errors";
import { useStaffFormat } from "../lib/format";
import { Fields } from "./field-group";
import { MilestoneList } from "./milestone-list";

export interface CaseCardProps {
  sickCase: StaffCase;
  /** The card's name: whose case, and from when. */
  title: string;
  /** Mark done and Waive: a manager, an Approver and HR — never the person. */
  canTick?: boolean;
  /**
   * Whether the accommodation note may be written here: the manager's and
   * HR's (`staff:manage`, `staff:personal-data`), never the Approver's, who is
   * not sent it either.
   */
  canChangeNote?: boolean;
  /** HR's (`staff:manage`): the 52-week estimate and *Close the case*. */
  hr?: boolean;
  /** staff:personal-data's: the milestones' notes. */
  canWriteNotes?: boolean;
}

/**
 * A sick-leave case as the caller reads it (design S10): its days, the
 * 52-week estimate, the milestones, the accommodation note when the response
 * carries it, and what the caller may do — each offered by the capability
 * the page hands in; the server judges every write again.
 */
export const CaseCard = ({
  sickCase,
  title,
  canTick = true,
  canChangeNote = true,
  hr = false,
  canWriteNotes = false,
}: CaseCardProps) => {
  const { t, date } = useStaffFormat();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<"note" | "estimate" | undefined>();
  const close = useMutation({
    mutationFn: () => closeCase(sickCase.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("caseClosed"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotSave"), message: refusalText(error, t) }),
  });
  const confirmClose = () =>
    modals.openConfirmModal({
      title: t("closeCaseTitle"),
      children: <Text size="sm">{t("closeCaseExplained")}</Text>,
      labels: { confirm: t("closeCaseConfirm"), cancel: t("cancel") },
      onConfirm: () => close.mutate(),
    });
  const showNote = sickCase.accommodationNote !== undefined || canChangeNote;
  return (
    <Card withBorder radius="md" padding="sm" component="article" aria-label={title}>
      <Stack gap="xs">
        <Text fw={500}>{title}</Text>
        <Fields
          items={[
            [t("caseLastDay"), date(sickCase.lastDay)],
            [
              t("caseMaxBenefit"),
              sickCase.maxBenefitEstimateEdited
                ? t("caseMaxBenefitEdited", { date: date(sickCase.maxBenefitEstimate) })
                : date(sickCase.maxBenefitEstimate),
            ],
          ]}
        />
        <MilestoneList sickCase={sickCase} canChange={canTick} canWriteNotes={canWriteNotes} />
        {showNote && (
          <Stack gap={0}>
            <Text size="xs" c="dimmed">
              {t("accommodationNote")}
            </Text>
            {sickCase.accommodationNote && <Text size="sm">{sickCase.accommodationNote}</Text>}
          </Stack>
        )}
        <Group gap="xs" wrap="wrap">
          {canChangeNote && (
            <Button size="xs" variant="light" leftSection={<IconPencil size={14} />} onClick={() => setEditing("note")}>
              {t("changeAccommodationNote")}
            </Button>
          )}
          {hr && (
            <Button size="xs" variant="light" onClick={() => setEditing("estimate")}>
              {t("changeEstimate")}
            </Button>
          )}
          {hr && sickCase.open && (
            <Button size="xs" variant="subtle" color="red" loading={close.isPending} onClick={confirmClose}>
              {t("closeCase")}
            </Button>
          )}
        </Group>
      </Stack>
      <Modal
        opened={editing !== undefined}
        onClose={() => setEditing(undefined)}
        title={editing === "estimate" ? t("caseMaxBenefit") : t("accommodationNote")}
      >
        {editing === "note" && <AccommodationNoteForm sickCase={sickCase} onDone={() => setEditing(undefined)} />}
        {editing === "estimate" && <EstimateForm sickCase={sickCase} onDone={() => setEditing(undefined)} />}
      </Modal>
    </Card>
  );
};

/** Saves one change of the case with its revision; a newer save closes the form on what is there, never overwritten. */
const useCaseSave = <T,>(
  saved: string,
  save: (value: T) => Promise<StaffCase>,
  onDone: () => void,
  onRefused: (words: string) => void,
) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: save,
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t(saved), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        // Somebody saved the case meanwhile: close on what is there now, so a
        // second Save never overwrites what the other writer wrote.
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        onDone();
        return;
      }
      onRefused(refusalText(error, t));
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "cases"] });
    },
  });
};

/** The accommodation note, sent with the case's revision. */
const AccommodationNoteForm = ({ sickCase, onDone }: { sickCase: StaffCase; onDone: () => void }) => {
  const { t } = useStaffFormat();
  const form = useForm({ initialValues: { note: sickCase.accommodationNote ?? "" } });
  const save = useCaseSave(
    "accommodationNoteSaved",
    (note: string) => updateAccommodationNote(sickCase, note),
    onDone,
    (words) => form.setErrors({ note: words }),
  );
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values.note))}>
      <Stack>
        <Textarea
          label={t("accommodationNote")}
          description={t("accommodationNoteDescription")}
          rows={3}
          maxLength={2000}
          {...form.getInputProps("note")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** HR's estimate of the 52-week end, or back to the derived first day + 364. */
const EstimateForm = ({ sickCase, onDone }: { sickCase: StaffCase; onDone: () => void }) => {
  const { t } = useStaffFormat();
  const form = useForm<{ estimate: string | null }>({
    initialValues: { estimate: sickCase.maxBenefitEstimate },
    validate: { estimate: (value) => (value ? null : t("estimateRequired")) },
  });
  const save = useCaseSave(
    "estimateSaved",
    (estimate: string | null) => updateMaxBenefitEstimate(sickCase, estimate),
    onDone,
    (words) => form.setErrors({ estimate: words }),
  );
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values.estimate))}>
      <Stack>
        <DateInput
          label={t("caseMaxBenefit")}
          description={t("estimateDescription")}
          valueFormat={t("dateInputFormat")}
          minDate={sickCase.firstDay}
          {...form.getInputProps("estimate")}
        />
        <Group justify="space-between" wrap="wrap">
          <Button type="button" variant="subtle" onClick={() => save.mutate(null)}>
            {t("resetEstimate")}
          </Button>
          <Group gap="xs">
            <Button type="button" variant="default" onClick={onDone}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        </Group>
      </Stack>
    </form>
  );
};
