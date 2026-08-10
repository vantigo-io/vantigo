import { Button, Group, Modal, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiValidationError, replaceMeter } from "../api/energy";

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
  const client = useQueryClient();
  const form = useForm<Values>({
    initialValues: initialValues(),
    validate: {
      meterNumber: (value) => (value.trim() ? null : "Meter number is required"),
      installedAt: (value) => (value ? null : "Installation time is required"),
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
      else notifications.show({ color: "red", title: "Could not replace meter", message: error.message });
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title="Replace meter" centered>
      <form
        onSubmit={form.onSubmit((values) => mutation.mutate({ ...values, meterNumber: values.meterNumber.trim() }))}
      >
        <Stack>
          <TextInput label="New meter number" withAsterisk {...form.getInputProps("meterNumber")} />
          <TextInput label="Installed at" type="datetime-local" withAsterisk {...form.getInputProps("installedAt")} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              Replace meter
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
