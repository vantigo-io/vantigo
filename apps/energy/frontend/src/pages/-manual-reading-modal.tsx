import { Button, Group, Modal, NumberInput, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { addConsumption } from "../api/energy";

export const ManualReadingModal = ({
  meteringPointId,
  opened,
  onClose,
}: {
  meteringPointId: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const client = useQueryClient();
  const form = useForm({
    initialValues: { start: "", end: "", quantityKwh: "" },
    validate: {
      start: (value) => (value ? null : "Start is required"),
      end: (value) => (value ? null : "End is required"),
      quantityKwh: (value) => (Number(value) >= 0 ? null : "Quantity must be zero or greater"),
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
  });
  return (
    <Modal opened={opened} onClose={onClose} title="Add manual reading" centered>
      <form
        onSubmit={form.onSubmit((values) =>
          mutation.mutate({ start: values.start, end: values.end, quantityKwh: Number(values.quantityKwh) }),
        )}
      >
        <Stack>
          <TextInput label="Start" type="datetime-local" withAsterisk {...form.getInputProps("start")} />
          <TextInput label="End" type="datetime-local" withAsterisk {...form.getInputProps("end")} />
          <NumberInput label="Quantity (kWh)" min={0} withAsterisk {...form.getInputProps("quantityKwh")} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              Add reading
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
