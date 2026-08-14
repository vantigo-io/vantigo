import { Button, Group, Modal, NumberInput, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { addConsumption } from "../api/energy";
import "../i18n";

export const ManualReadingModal = ({
  meteringPointId,
  opened,
  onClose,
}: {
  meteringPointId: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("energy");
  const client = useQueryClient();
  const form = useForm({
    initialValues: { start: "", end: "", quantityKwh: "" },
    validate: {
      start: (value) => (value ? null : t("startRequired")),
      end: (value) => (value ? null : t("endRequired")),
      quantityKwh: (value) => (Number(value) >= 0 ? null : t("quantityZeroOrGreater")),
    },
  });
  const mutation = useMutation({
    mutationFn: (values: { start: string; end: string; quantityKwh: number }) =>
      addConsumption(meteringPointId, values),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "consumption"] });
      form.reset();
      onClose();
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotAddReading"), message: error.message }),
  });
  return (
    <Modal opened={opened} onClose={onClose} title={t("manualReadingTitle")} centered>
      <form
        onSubmit={form.onSubmit((values) =>
          mutation.mutate({ start: values.start, end: values.end, quantityKwh: Number(values.quantityKwh) }),
        )}
      >
        <Stack>
          <TextInput label={t("start")} type="datetime-local" withAsterisk {...form.getInputProps("start")} />
          <TextInput label={t("end")} type="datetime-local" withAsterisk {...form.getInputProps("end")} />
          <NumberInput label={t("quantityKwh")} min={0} withAsterisk {...form.getInputProps("quantityKwh")} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {t("addManualReading")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
