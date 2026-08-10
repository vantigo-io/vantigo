import { Alert, Button, Group, Modal, Select, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { customersQueryOptions } from "../api/customers";
import { ApiValidationError, switchSupplyPeriod } from "../api/energy";

export const SupplyPeriodModal = ({
  meteringPointId,
  opened,
  onClose,
  hasActivePeriod = false,
}: {
  meteringPointId: number;
  opened: boolean;
  onClose: () => void;
  hasActivePeriod?: boolean;
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
    mutationFn: (values: { customerId: number; start: string }) => switchSupplyPeriod(meteringPointId, values),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "supply-periods"] });
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "consumption"] });
      void client.invalidateQueries({ queryKey: ["energy", "customers"] });
      form.reset();
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
    },
  });
  const action = hasActivePeriod ? "Switch customer" : "Assign customer";
  return (
    <Modal opened={opened} onClose={onClose} title={action} centered>
      <form
        onSubmit={form.onSubmit((values) =>
          mutation.mutate({ customerId: Number(values.customerId), start: values.start }),
        )}
      >
        <Stack>
          {mutation.error && <Alert color="red">{mutation.error.message}</Alert>}
          <Select
            label="Customer"
            withAsterisk
            searchable
            searchValue={search}
            onSearchChange={setSearch}
            data={(customers?.data ?? []).map((customer) => ({ value: String(customer.id), label: customer.name }))}
            {...form.getInputProps("customerId")}
          />
          <TextInput label="Switch date" type="date" withAsterisk {...form.getInputProps("start")} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {action}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
