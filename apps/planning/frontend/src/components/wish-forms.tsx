import { Alert, Button, Drawer, em, Group, Modal, Select, Stack } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { PlanningProjectRef } from "../api/projects";
import { ApiValidationError, PLANNING_QUERY_KEY, refusalCode, ruleRefusals } from "../api/request";
import { createMyWish, type PlanningWishKind } from "../api/wishes";
import { usePlanningFormat } from "../lib/format";
import { today } from "../lib/weeks";

export interface WishDialogProps {
  /** The kind of wish being filed; left out, the dialog is closed. */
  kind?: PlanningWishKind;
  onClose: () => void;
  /** The projects an `off` wish may name: those of the person's confirmed bookings shown. */
  projects: readonly PlanningProjectRef[];
}

/**
 * Filing a wish of one's own (design S21, §7), phone first: a small dialog —
 * a bottom sheet on a phone — asking only for what the wish is: for *off*,
 * the project (one of the person's confirmed bookings) and the day; for
 * *new*, the day alone. No free text. The rules the server judges —
 * no booking on the project, five open wishes, one off a project already —
 * are said inside the dialog in their own words.
 */
export const WishDialog = ({ kind, onClose, projects }: WishDialogProps) => {
  const { t } = usePlanningFormat();
  const phone = useMediaQuery(`(max-width: ${em(767.98)})`);
  const title = kind === "off" ? t("wishOffTitle") : t("wishNewTitle");
  const body = kind && <WishForm key={kind} kind={kind} projects={projects} onDone={onClose} />;
  return phone ? (
    <Drawer
      opened={kind !== undefined}
      onClose={onClose}
      title={title}
      position="bottom"
      size="auto"
      closeButtonProps={{ "aria-label": t("close") }}
    >
      {body}
    </Drawer>
  ) : (
    <Modal opened={kind !== undefined} onClose={onClose} title={title} closeButtonProps={{ "aria-label": t("close") }}>
      {body}
    </Modal>
  );
};

interface WishValues {
  projectId: string | null;
  fromDate: string | null;
}

const WishForm = ({
  kind,
  projects,
  onDone,
}: {
  kind: PlanningWishKind;
  projects: readonly PlanningProjectRef[];
  onDone: () => void;
}) => {
  const { t, projectName } = usePlanningFormat();
  const queryClient = useQueryClient();
  const [refusal, setRefusal] = useState<string>();
  const form = useForm<WishValues>({
    initialValues: { projectId: projects.length === 1 ? String(projects[0].id) : null, fromDate: null },
    validate: {
      projectId: (value) => (kind === "off" && value === null ? t("projectRequired") : null),
      fromDate: (value) => (value ? null : t("wishDateRequired")),
    },
  });

  const send = useMutation({
    mutationFn: (values: WishValues) =>
      createMyWish({
        kind,
        ...(kind === "off" ? { projectId: Number(values.projectId) } : {}),
        fromDate: values.fromDate as string,
      }),
    onMutate: () => setRefusal(undefined),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("wishSent"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
      onDone();
    },
    onError: (error: Error) => {
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        form.setErrors({ projectId: fields.projectId ?? fields.kind, fromDate: fields.fromDate });
        return;
      }
      const code = refusalCode(error);
      setRefusal(code && ruleRefusals.includes(code) ? t(`refusal.${code}`) : error.message);
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => send.mutate(values))}>
      <Stack>
        {kind === "off" && (
          <Select
            label={t("project")}
            withAsterisk
            allowDeselect={false}
            data={projects.map((project) => ({ value: String(project.id), label: projectName(project) }))}
            {...form.getInputProps("projectId")}
          />
        )}
        <DateInput
          label={t("wishFrom")}
          withAsterisk
          minDate={today()}
          valueFormat={t("dateInputFormat")}
          {...form.getInputProps("fromDate")}
        />
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSendWish")}>
            {refusal}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={send.isPending}>
            {t("send")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
