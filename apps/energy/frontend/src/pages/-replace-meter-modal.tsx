import { Button, Group, Modal, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { ApiValidationError, replaceMeter } from "../api/energy";
import "../i18n";

type Values = { meterNumber: string; installedAt: string };

const initialValues = (): Values => ({
  meterNumber: "",
  installedAt: new Date().toISOString().slice(0, 16),
});

export const ReplaceMeterModal = ({
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
  const form = useForm<Values>({
    initialValues: initialValues(),
    validate: {
      meterNumber: (value) => (value.trim() ? null : t("meterNumberRequired")),
      installedAt: (value) => (value ? null : t("installationTimeRequired")),
    },
  });
  const mutation = useMutation({
    mutationFn: (values: Values) =>
      replaceMeter(meteringPointId, { ...values, installedAt: new Date(values.installedAt).toISOString() }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "meters"] });
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId] });
      form.reset();
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      else notifications.show({ color: "red", title: t("couldNotReplaceMeter"), message: error.message });
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("replaceMeterTitle")} centered>
      <form
        onSubmit={form.onSubmit((values) => mutation.mutate({ ...values, meterNumber: values.meterNumber.trim() }))}
      >
        <Stack>
          <TextInput label={t("newMeterNumber")} withAsterisk {...form.getInputProps("meterNumber")} />
          <TextInput
            label={t("installedAt")}
            type="datetime-local"
            withAsterisk
            {...form.getInputProps("installedAt")}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {t("replaceMeter")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
