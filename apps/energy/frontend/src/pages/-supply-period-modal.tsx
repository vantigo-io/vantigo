import { Button, Group, Modal, Select, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { customersQueryOptions } from "../api/customers";
import { assignSupplyPeriod } from "../api/energy";

export const SupplyPeriodModal = ({
  meteringPointId,
  opened,
  onClose,
}: {
  meteringPointId: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const { data: customers } = useQuery({ ...customersQueryOptions(search), enabled: opened });
  const form = useForm({
    initialValues: { customerId: "", start: "" },
    validate: {
      customerId: (value) => (value ? null : "Customer is required"),
      start: (value) => (value ? null : "Start is required"),
    },
  });
  const mutation = useMutation({
    mutationFn: (values: { customerId: number; start: string }) => assignSupplyPeriod(meteringPointId, values),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "supply-periods"] });
      form.reset();
      onClose();
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: "Could not assign customer",
        message:
          (error as { status?: number }).status === 409
            ? "This metering point already has an overlapping supply period."
            : error.message,
      }),
  });
  return (
    <Modal opened={opened} onClose={onClose} title="Assign customer" centered>
      <form
        onSubmit={form.onSubmit((values) =>
          mutation.mutate({ customerId: Number(values.customerId), start: values.start }),
        )}
      >
        <Stack>
          <Select
            label="Customer"
            withAsterisk
            searchable
            searchValue={search}
            onSearchChange={setSearch}
            data={(customers?.data ?? []).map((customer) => ({ value: String(customer.id), label: customer.name }))}
            {...form.getInputProps("customerId")}
          />
          <TextInput label="Start" type="date" withAsterisk {...form.getInputProps("start")} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              Assign customer
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
