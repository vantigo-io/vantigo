import { Alert, Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconLock, IconLockOpen } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type StaffPerson, setLegalHold } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useStaffFormat } from "../lib/format";

const REASON_MAX = 500;

/**
 * The legal hold as the hold group's readers see it (design S15, class F):
 * `staff:manage` and `staff:personal-data` read `legalHold`, so its presence
 * says the reader may see it; nobody else is told a hold exists.
 */
export const LegalHoldAlert = ({ person, timeZone }: { person: StaffPerson; timeZone: string }) => {
  const format = useStaffFormat();
  const t = format.t;
  const hold = person.legalHold;
  if (!hold?.held) return null;
  return (
    <Alert color="orange" icon={<IconLock size={16} />} title={t("legalHoldTitle")}>
      <Stack gap={4}>
        <Text size="sm">
          {t("legalHoldSince", { date: hold.at ? format.day(hold.at, timeZone) : t("notSet"), reason: hold.reason })}
        </Text>
        <Text size="sm" c="dimmed">
          {t("legalHoldExplained")}
        </Text>
      </Stack>
    </Alert>
  );
};

/**
 * Sets or lifts the hold — `staff:personal-data`'s (`canHold`), and one of
 * the writes an erased person still takes. Setting asks for the reason;
 * lifting asks first and sends none.
 */
export const LegalHoldButton = ({ person }: { person: StaffPerson }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const [setting, setSetting] = useState(false);
  const held = person.legalHold?.held === true;

  const lift = useMutation({
    mutationFn: () => setLegalHold(person.id, false),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("legalHoldLifted"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "people"] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotChangeLegalHold"), message: refusalMessage(error) }),
  });

  const confirmLift = () =>
    modals.openConfirmModal({
      title: t("liftLegalHoldTitle", { name: person.displayName }),
      children: <Text size="sm">{t("liftLegalHoldExplained")}</Text>,
      labels: { confirm: t("liftLegalHold"), cancel: t("cancel") },
      confirmProps: { color: "orange" },
      onConfirm: () => lift.mutate(),
    });

  return (
    <>
      {held ? (
        <Button
          variant="light"
          color="orange"
          leftSection={<IconLockOpen size={16} />}
          loading={lift.isPending}
          onClick={confirmLift}
        >
          {t("liftLegalHold")}
        </Button>
      ) : (
        <Button variant="light" leftSection={<IconLock size={16} />} onClick={() => setSetting(true)}>
          {t("putUnderLegalHold")}
        </Button>
      )}
      <HoldModal person={person} opened={setting} onClose={() => setSetting(false)} />
    </>
  );
};

const HoldModal = ({ person, opened, onClose }: { person: StaffPerson; opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={t("putUnderLegalHoldTitle", { name: person.displayName })}
      fullScreen={phone}
    >
      {opened && <HoldForm person={person} onDone={onClose} />}
    </Modal>
  );
};

const HoldForm = ({ person, onDone }: { person: StaffPerson; onDone: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const form = useForm<{ reason: string }>({
    initialValues: { reason: "" },
    validate: {
      reason: (value) =>
        !value.trim()
          ? t("holdReasonRequired")
          : value.trim().length > REASON_MAX
            ? t("textTooLong", { max: REASON_MAX })
            : null,
    },
  });
  const save = useMutation({
    mutationFn: (values: { reason: string }) => setLegalHold(person.id, true, values.reason.trim()),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("legalHoldSet"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "people"] });
      onDone();
    },
    onError: (error) => {
      const { fields, rest } = fieldRefusals(error, ["reason"]);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotChangeLegalHold"), message: rest.join(" ") });
    },
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("putUnderLegalHoldExplained")}
        </Text>
        <Textarea
          label={t("holdReason")}
          description={t("holdReasonDescription")}
          withAsterisk
          rows={3}
          data-autofocus
          {...form.getInputProps("reason")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("setLegalHold")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
