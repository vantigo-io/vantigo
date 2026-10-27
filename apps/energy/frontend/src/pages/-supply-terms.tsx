import { Alert, Button, Group, Modal, Select, Stack, TextInput } from "@mantine/core";
import { type GetInputPropsReturnType, useForm } from "@mantine/form";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import { ApiValidationError, BILLING_CADENCES, type BillingCadence, type SupplyPeriod } from "../api/energy";
import { updateSupplyPeriodTerms } from "../api/tariffs";
import { cadenceKey, tariffIdOf, useTariffOptions } from "../lib/billing";
import "../i18n";

export interface SupplyTermsValues {
  tariffId: string | null;
  cadence: BillingCadence;
}

const TERMS_FIELDS: ReadonlySet<string> = new Set(["tariffId", "cadence"]);

/**
 * The power tariff and the billing cadence of a supply period. Without
 * `canViewTariffs` the tariff is shown read-only and stays what it is.
 */
export const SupplyTermsFields = ({
  form,
  opened,
  canViewTariffs,
  held = { id: null, name: null },
}: {
  form: { getInputProps: (path: "tariffId" | "cadence") => GetInputPropsReturnType };
  opened: boolean;
  canViewTariffs: boolean;
  held?: { id: number | null; name: string | null };
}) => {
  const { t } = useI18n("energy");
  const options = useTariffOptions("power", opened && canViewTariffs, held);
  return (
    <>
      {canViewTariffs ? (
        <Select
          label={t("powerTariff")}
          description={t("powerTariffHint")}
          placeholder={t("noPowerTariff")}
          clearable
          data={options}
          {...form.getInputProps("tariffId")}
        />
      ) : (
        <TextInput label={t("powerTariff")} readOnly value={held.name ?? t("noPowerTariff")} />
      )}
      <Select
        label={t("billingCadence")}
        description={t("billingCadenceHint")}
        allowDeselect={false}
        data={BILLING_CADENCES.map((cadence) => ({ value: cadence, label: t(cadenceKey(cadence)) }))}
        {...form.getInputProps("cadence")}
      />
    </>
  );
};

/**
 * **Change terms**: a supply period's power tariff and cadence, nothing else
 * of it (energy tariffs plan, reading 6). The tariff it already holds stays
 * selectable even archived.
 */
export const SupplyTermsModal = ({
  meteringPointId,
  period,
  onClose,
  canViewTariffs = false,
}: {
  meteringPointId: number;
  period: SupplyPeriod | null;
  onClose: () => void;
  canViewTariffs?: boolean;
}) => {
  const { t } = useI18n("energy");
  const client = useQueryClient();
  const form = useForm<SupplyTermsValues>({ initialValues: { tariffId: null, cadence: "monthly" } });
  useEffect(() => {
    if (!period) return;
    form.setValues({
      tariffId: period.tariffId != null ? String(period.tariffId) : null,
      cadence: period.cadence ?? "monthly",
    });
    form.clearErrors();
    // The form is reset only when the dialog opens on a period.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [period]);
  const mutation = useMutation({
    mutationFn: (values: SupplyTermsValues) => {
      if (!period) throw new Error("No period");
      return updateSupplyPeriodTerms(meteringPointId, period.id, {
        tariffId: canViewTariffs ? tariffIdOf(values.tariffId) : (period.tariffId ?? null),
        cadence: values.cadence,
      });
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "supply-periods"] });
      void client.invalidateQueries({ queryKey: ["energy", "customers"] });
      void client.invalidateQueries({ queryKey: ["energy", "tariffs"] });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
    },
  });
  // A refusal the two fields cannot show — one naming no field (a cancelled
  // period) or another field — is shown above them.
  const error = mutation.error;
  const unplaced = !error
    ? []
    : error instanceof ApiValidationError
      ? Object.entries(error.fieldErrors)
          .filter(([field]) => !TERMS_FIELDS.has(field))
          .map(([, message]) => message)
      : [error.message];
  return (
    <Modal opened={period !== null} onClose={onClose} title={t("changeSupplyTerms")} centered>
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          {unplaced.length > 0 && <Alert color="red">{unplaced.join(" ")}</Alert>}
          <SupplyTermsFields
            form={form}
            opened={period !== null}
            canViewTariffs={canViewTariffs}
            held={{ id: period?.tariffId ?? null, name: period?.tariffName ?? null }}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {t("saveChanges")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
