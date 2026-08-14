import { Button, Group, Modal, Select, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { assignSupplyPeriod, meteringPointsQueryOptions } from "../api/energy";
import "../i18n";

export const AttachMeteringPointModal = ({
  customerId,
  opened,
  onClose,
}: {
  customerId: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("energy");
  const client = useQueryClient();
  const [searchInput, setSearchInput] = useState("");
  const [search] = useDebouncedValue(searchInput, 250);
  const { data } = useQuery({ ...meteringPointsQueryOptions({ page: 1, pageSize: 50, search }), enabled: opened });
  const form = useForm({
    initialValues: { meteringPointId: "", start: "" },
    validate: {
      meteringPointId: (value) => (value ? null : t("meteringPointRequired")),
      start: (value) => (value ? null : t("startRequired")),
    },
  });
  const mutation = useMutation({
    mutationFn: (values: { meteringPointId: number; start: string }) =>
      assignSupplyPeriod(values.meteringPointId, { customerId, start: values.start }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["energy", "customers", customerId, "metering-points"] });
      form.reset();
      onClose();
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("couldNotAttachMeteringPoint"),
        message: (error as { status?: number }).status === 409 ? t("overlappingSupplyPeriod") : error.message,
      }),
  });
  return (
    <Modal opened={opened} onClose={onClose} title={t("attachMeteringPoint")} centered>
      <form
        onSubmit={form.onSubmit((values) =>
          mutation.mutate({
            meteringPointId: Number(values.meteringPointId),
            // The date input yields a plain "YYYY-MM-DD"; the backend requires
            // an explicit UTC timestamp, so send UTC midnight of that day.
            start: new Date(values.start).toISOString(),
          }),
        )}
      >
        <Stack>
          <Select
            label={t("meteringPoints")}
            placeholder={t("searchGsrnOrMeterNumber")}
            withAsterisk
            searchable
            searchValue={searchInput}
            onSearchChange={setSearchInput}
            data={(data?.data ?? []).map((point) => ({
              value: String(point.id),
              label: `${point.gsrn} · ${point.meterNumber ?? t("notAvailable")}`,
            }))}
            {...form.getInputProps("meteringPointId")}
          />
          <TextInput label={t("start")} type="date" withAsterisk {...form.getInputProps("start")} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {t("attachMeteringPoint")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
