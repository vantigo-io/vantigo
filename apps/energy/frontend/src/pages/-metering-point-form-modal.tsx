import { Button, Group, Modal, NumberInput, Select, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import {
  ApiValidationError,
  createMeteringPoint,
  type MeteringPoint,
  type MeteringPointInput,
  type MeteringPointUpdateInput,
  updateMeteringPoint,
} from "../api/energy";

export type MeteringPointModalState = { mode: "create" } | { mode: "edit"; meteringPoint: MeteringPoint };

type Values = {
  gsrn: string;
  meterNumber: string;
  streetAddress: string;
  postalCode: string;
  city: string;
  countryCode: string;
  priceArea: string;
  gridArea: string;
  expectedAnnualConsumptionKwh: number | string;
  latitude: number | string;
  longitude: number | string;
  connectionStatus: "New" | "Connected" | "Disconnected";
};

const empty: Values = {
  gsrn: "",
  meterNumber: "",
  streetAddress: "",
  postalCode: "",
  city: "",
  countryCode: "NO",
  priceArea: "NO1",
  gridArea: "",
  expectedAnnualConsumptionKwh: "",
  latitude: "",
  longitude: "",
  connectionStatus: "New",
};

const fromState = (state: MeteringPointModalState): Values => {
  if (state.mode === "create") return empty;
  const point = state.meteringPoint;
  return {
    gsrn: point.gsrn,
    meterNumber: point.meterNumber ?? "",
    streetAddress: point.address.streetAddress,
    postalCode: point.address.postalCode,
    city: point.address.city,
    countryCode: point.address.countryCode,
    priceArea: point.priceArea,
    gridArea: point.gridArea ?? "",
    expectedAnnualConsumptionKwh: point.expectedAnnualConsumptionKwh ?? "",
    latitude: point.latitude ?? "",
    longitude: point.longitude ?? "",
    connectionStatus: point.connectionStatus,
  };
};

export const MeteringPointFormModal = ({
  state,
  onClose,
}: {
  state: MeteringPointModalState | null;
  onClose: () => void;
}) => {
  const client = useQueryClient();
  const isEdit = state?.mode === "edit";
  const form = useForm<Values>({
    initialValues: empty,
    validate: {
      gsrn: (value) => (/^\d{18}$/.test(value.trim()) ? null : "GSRN must contain exactly 18 digits"),
      meterNumber: (value) => (isEdit || value.trim() ? null : "Meter number is required"),
      streetAddress: (value) => (value.trim() ? null : "Street address is required"),
      postalCode: (value) => (value.trim() ? null : "Postal code is required"),
      city: (value) => (value.trim() ? null : "City is required"),
      countryCode: (value) => (/^[A-Za-z]{2}$/.test(value.trim()) ? null : "Use a two-letter country code"),
    },
  });

  useEffect(() => {
    if (state) {
      form.setValues(fromState(state));
      form.resetDirty();
      form.clearErrors();
    }
    // The form is intentionally reset only when the modal state changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  const mutation = useMutation({
    mutationFn: (input: MeteringPointInput | MeteringPointUpdateInput) =>
      isEdit
        ? updateMeteringPoint(state.meteringPoint.id, input as MeteringPointUpdateInput)
        : createMeteringPoint(input as MeteringPointInput),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["energy", "metering-points"] });
      notifications.show({
        color: "teal",
        title: isEdit ? "Metering point updated" : "Metering point created",
        message: "The metering point was saved.",
      });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      else notifications.show({ color: "red", title: "Could not save metering point", message: error.message });
    },
  });

  const submit = form.onSubmit((values) => {
    const input = {
      gsrn: values.gsrn.trim(),
      address: {
        streetAddress: values.streetAddress.trim(),
        postalCode: values.postalCode.trim(),
        city: values.city.trim(),
        countryCode: values.countryCode.trim().toUpperCase(),
      },
      priceArea: values.priceArea as MeteringPointInput["priceArea"],
      gridArea: values.gridArea.trim() || undefined,
      expectedAnnualConsumptionKwh:
        values.expectedAnnualConsumptionKwh === "" ? undefined : Number(values.expectedAnnualConsumptionKwh),
      latitude: values.latitude === "" ? undefined : Number(values.latitude),
      longitude: values.longitude === "" ? undefined : Number(values.longitude),
      connectionStatus: values.connectionStatus,
    } satisfies MeteringPointUpdateInput;
    mutation.mutate(isEdit ? input : { ...input, meterNumber: values.meterNumber.trim() });
  });

  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={isEdit ? "Edit metering point" : "New metering point"}
      centered
    >
      <form onSubmit={submit}>
        <Stack>
          <TextInput label="GSRN" description="18 digits" withAsterisk data-autofocus {...form.getInputProps("gsrn")} />
          {!isEdit && <TextInput label="Meter number" withAsterisk {...form.getInputProps("meterNumber")} />}
          <TextInput label="Street address" withAsterisk {...form.getInputProps("streetAddress")} />
          <Group grow>
            <TextInput label="Postal code" withAsterisk {...form.getInputProps("postalCode")} />
            <TextInput label="City" withAsterisk {...form.getInputProps("city")} />
          </Group>
          <Group grow>
            <TextInput label="Country code" withAsterisk {...form.getInputProps("countryCode")} />
            <Select
              label="Price area"
              data={["NO1", "NO2", "NO3", "NO4", "NO5"]}
              {...form.getInputProps("priceArea")}
            />
          </Group>
          <Group grow>
            <TextInput label="Grid area" {...form.getInputProps("gridArea")} />
            <Select
              label="Connection status"
              data={["New", "Connected", "Disconnected"]}
              {...form.getInputProps("connectionStatus")}
            />
          </Group>
          <NumberInput
            label="Expected annual consumption (kWh)"
            min={0}
            {...form.getInputProps("expectedAnnualConsumptionKwh")}
          />
          <Group grow>
            <NumberInput label="Latitude" decimalScale={6} {...form.getInputProps("latitude")} />
            <NumberInput label="Longitude" decimalScale={6} {...form.getInputProps("longitude")} />
          </Group>
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? "Save changes" : "Create metering point"}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
