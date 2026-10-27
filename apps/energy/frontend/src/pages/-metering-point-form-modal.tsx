import { Button, Checkbox, Group, Modal, NumberInput, Select, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import {
  ApiValidationError,
  billingFacts,
  createMeteringPoint,
  ELECTRICITY_TAX_CLASSES,
  type ElectricityTaxClass,
  type MeteringPoint,
  type MeteringPointInput,
  type MeteringPointUpdateInput,
  updateMeteringPoint,
} from "../api/energy";
import { taxClassKey, useTariffOptions } from "../lib/billing";
import "../i18n";

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
  gridTariffId: string | null;
  household: boolean;
  electricityTaxClass: ElectricityTaxClass;
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
  gridTariffId: null,
  household: false,
  electricityTaxClass: "ordinary",
};

const fromState = (state: MeteringPointModalState): Values => {
  if (state.mode === "create") return empty;
  const point = state.meteringPoint;
  const facts = billingFacts(point);
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
    gridTariffId: facts.gridTariffId !== null ? String(facts.gridTariffId) : null,
    household: facts.household,
    electricityTaxClass: facts.electricityTaxClass,
  };
};

/**
 * Create or edit a metering point. The edit is a full replace, so the billing
 * facts — the grid tariff, household use and the tax class — are always sent.
 * `canViewTariffs` (`energy:tariffs-view`) lists the active grid tariffs, and
 * the one held even when archived; without it the held one is shown read-only
 * and sent unchanged.
 */
export const MeteringPointFormModal = ({
  state,
  onClose,
  canViewTariffs = false,
}: {
  state: MeteringPointModalState | null;
  onClose: () => void;
  canViewTariffs?: boolean;
}) => {
  const { t } = useI18n("energy");
  const client = useQueryClient();
  const isEdit = state?.mode === "edit";
  const held = isEdit
    ? { id: billingFacts(state.meteringPoint).gridTariffId, name: billingFacts(state.meteringPoint).gridTariffName }
    : { id: null, name: null };
  const gridTariffs = useTariffOptions("grid", state !== null && canViewTariffs, held);
  const form = useForm<Values>({
    initialValues: empty,
    validate: {
      gsrn: (value) => (/^\d{18}$/.test(value.trim()) ? null : t("gsrnInvalid")),
      meterNumber: (value) => (isEdit || value.trim() ? null : t("meterNumberRequired")),
      streetAddress: (value) => (value.trim() ? null : t("streetAddressRequired")),
      postalCode: (value) => (value.trim() ? null : t("postalCodeRequired")),
      city: (value) => (value.trim() ? null : t("cityRequired")),
      countryCode: (value) => (/^[A-Za-z]{2}$/.test(value.trim()) ? null : t("countryCodeInvalid")),
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
        title: isEdit ? t("meteringPointUpdated") : t("meteringPointCreated"),
        message: t("meteringPointSaved"),
      });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      else notifications.show({ color: "red", title: t("couldNotSaveMeteringPoint"), message: error.message });
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
      gridTariffId: canViewTariffs ? (values.gridTariffId ? Number(values.gridTariffId) : null) : held.id,
      household: values.household,
      electricityTaxClass: values.electricityTaxClass,
    } satisfies MeteringPointUpdateInput;
    mutation.mutate(isEdit ? input : { ...input, meterNumber: values.meterNumber.trim() });
  });

  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={isEdit ? t("editMeteringPointTitle") : t("newMeteringPointTitle")}
      centered
    >
      <form onSubmit={submit}>
        <Stack>
          <TextInput
            label="GSRN"
            description={t("gsrnDescription")}
            withAsterisk
            data-autofocus
            {...form.getInputProps("gsrn")}
          />
          {!isEdit && <TextInput label={t("meterNumber")} withAsterisk {...form.getInputProps("meterNumber")} />}
          <TextInput label={t("streetAddress")} withAsterisk {...form.getInputProps("streetAddress")} />
          <Group grow>
            <TextInput label={t("postalCode")} withAsterisk {...form.getInputProps("postalCode")} />
            <TextInput label={t("city")} withAsterisk {...form.getInputProps("city")} />
          </Group>
          <Group grow>
            <TextInput label={t("countryCode")} withAsterisk {...form.getInputProps("countryCode")} />
            <Select
              label={t("priceArea")}
              data={["NO1", "NO2", "NO3", "NO4", "NO5"]}
              {...form.getInputProps("priceArea")}
            />
          </Group>
          <Group grow>
            <TextInput label={t("gridArea")} {...form.getInputProps("gridArea")} />
            <Select
              label={t("connectionStatus")}
              data={[
                { value: "New", label: t("connectionStatusNew") },
                { value: "Connected", label: t("connectionStatusConnected") },
                { value: "Disconnected", label: t("connectionStatusDisconnected") },
              ]}
              {...form.getInputProps("connectionStatus")}
            />
          </Group>
          <NumberInput
            label={t("expectedAnnualConsumptionKwh")}
            min={0}
            {...form.getInputProps("expectedAnnualConsumptionKwh")}
          />
          <Group grow>
            <NumberInput label={t("latitude")} decimalScale={6} {...form.getInputProps("latitude")} />
            <NumberInput label={t("longitude")} decimalScale={6} {...form.getInputProps("longitude")} />
          </Group>
          <Text fw={600} size="sm">
            {t("billing")}
          </Text>
          {canViewTariffs ? (
            <Select
              label={t("gridTariff")}
              description={t("gridTariffHint")}
              placeholder={t("noGridTariff")}
              clearable
              data={gridTariffs}
              {...form.getInputProps("gridTariffId")}
            />
          ) : (
            <TextInput label={t("gridTariff")} readOnly value={held.name ?? t("noGridTariff")} />
          )}
          <Select
            label={t("electricityTaxClass")}
            allowDeselect={false}
            data={ELECTRICITY_TAX_CLASSES.map((value) => ({ value, label: t(taxClassKey(value)) }))}
            {...form.getInputProps("electricityTaxClass")}
          />
          <Checkbox
            label={t("householdUse")}
            description={t("householdUseHint")}
            {...form.getInputProps("household", { type: "checkbox" })}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? t("saveChanges") : t("createMeteringPoint")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
