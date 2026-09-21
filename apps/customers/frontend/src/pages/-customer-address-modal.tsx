import { Alert, Button, Checkbox, Group, Modal, Select, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import {
  ADDRESS_TYPE_ORDER,
  ApiValidationError,
  type CustomerAddress,
  type CustomerAddressInput,
  createCustomerAddress,
  customerAddressesQueryOptions,
  updateCustomerAddress,
} from "../api/addresses";
import { customerBillingProfileQueryOptions } from "../api/billing-profile";
import { CountrySelect } from "../components/country-select";
import { addressTypeLabel } from "../lib/address-type-label";
import "../i18n";

export type AddressModalState = { mode: "add" } | { mode: "edit"; address: CustomerAddress } | null;

interface AddressFormValues {
  type: string;
  label: string;
  line1: string;
  line2: string;
  postalCode: string;
  city: string;
  region: string;
  country: string;
  isPrimary: boolean;
}

const emptyValues: AddressFormValues = {
  type: "invoice",
  label: "",
  line1: "",
  line2: "",
  postalCode: "",
  city: "",
  region: "",
  country: "no",
  isPrimary: false,
};

const valuesFromAddress = (address: CustomerAddress): AddressFormValues => ({
  type: address.type,
  label: address.label ?? "",
  line1: address.line1,
  line2: address.line2 ?? "",
  postalCode: address.postalCode ?? "",
  city: address.city ?? "",
  region: address.region ?? "",
  country: address.country,
  isPrimary: address.isPrimary,
});

/**
 * Adds or edits one of a customer's typed addresses (design D1, D3): a full
 * replace, so `mutationFn` always sends every field. `addresses` is the
 * customer's current list, passed down by `CustomerAddressesSection` (it
 * already holds the query) so this modal can compute the primary checkbox's
 * forced state without a query of its own.
 */
export const CustomerAddressModal = ({
  customerId,
  addresses,
  state,
  onClose,
}: {
  customerId: number;
  addresses: CustomerAddress[];
  state: AddressModalState;
  onClose: () => void;
}) => {
  const { t, locale } = useI18n("customers");
  const queryClient = useQueryClient();
  const isEdit = state?.mode === "edit";
  const [capError, setCapError] = useState<string | null>(null);

  const form = useForm<AddressFormValues>({
    initialValues: emptyValues,
    validate: {
      line1: (value) => (value.trim() ? null : t("addressLine1Required")),
    },
  });

  // Re-seeds the form from `state` on mount and whenever it changes —
  // opening the modal, switching from add to edit or between two addresses —
  // the same `useEffect` `CustomerFormModal` uses for its own `state`
  // (an effect, not a render-time adjustment: this must also run on the very
  // first render, when `state` already opens the modal directly).
  useEffect(() => {
    if (state) {
      form.setValues(state.mode === "edit" ? valuesFromAddress(state.address) : emptyValues);
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);
  // The cap alert is not part of the form (design D3's "addresses" error has
  // no input of its own — see onError below), so it is reset the same way
  // CustomerFormModal resets its own non-form conflict state: a render-time
  // adjustment on `state` changing, not inside the effect above.
  const [seenState, setSeenState] = useState(state);
  if (state !== seenState) {
    setSeenState(state);
    if (capError) setCapError(null);
  }

  // Design D3: the first address of a type is primary whatever the request
  // says, and an address that is currently the only or primary one of its
  // type cannot be un-primaried through this checkbox — only by making
  // another one primary. Both cases force the checkbox on and disable it.
  const otherOfType = addresses.filter(
    (address) => address.type === form.values.type && !(isEdit && state.address.id === address.id),
  );
  const isCurrentPrimary = isEdit && state.address.isPrimary && state.address.type === form.values.type;
  const forcedPrimary = otherOfType.length === 0 || isCurrentPrimary;

  const mutation = useMutation({
    mutationFn: (values: AddressFormValues) => {
      const input: CustomerAddressInput = {
        type: values.type,
        label: values.label.trim() || null,
        line1: values.line1.trim(),
        line2: values.line2.trim() || null,
        postalCode: values.postalCode.trim() || null,
        city: values.city.trim() || null,
        region: values.region.trim() || null,
        country: values.country,
        isPrimary: forcedPrimary || values.isPrimary,
      };
      return isEdit
        ? updateCustomerAddress(customerId, state.address.id, input)
        : createCustomerAddress(customerId, input);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: customerAddressesQueryOptions(customerId).queryKey });
      // The billing profile's warnings are computed from the customer's
      // addresses (design D4), so the first invoice address added here
      // settles `no_invoice_address` — see `CustomerAddressesSection`'s own
      // invalidation. The customer row itself does not move (design D3).
      queryClient.invalidateQueries({ queryKey: customerBillingProfileQueryOptions(customerId).queryKey });
      onClose();
      notifications.show({
        color: "teal",
        title: isEdit ? t("addressUpdated") : t("addressAdded"),
        message: t("addressSaved"),
      });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        // "addresses" is the 50-address cap's key (design D3) — no input of
        // its own to attach to, so it is shown as a form-level alert instead.
        const { addresses: capMessage, ...fieldErrors } = error.fieldErrors;
        form.setErrors(fieldErrors);
        setCapError(capMessage ?? null);
        return;
      }
      notifications.show({ color: "red", title: t("addressCouldNotBeSaved"), message: error.message });
    },
  });

  return (
    <Modal opened={state !== null} onClose={onClose} title={isEdit ? t("editAddress") : t("addAddress")} centered>
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          {capError && <Alert color="red">{capError}</Alert>}
          <Select
            label={t("addressTypeLabel")}
            data={ADDRESS_TYPE_ORDER.map((type) => ({ value: type, label: addressTypeLabel(t, type) }))}
            allowDeselect={false}
            {...form.getInputProps("type")}
          />
          <TextInput
            label={t("addressLabel")}
            placeholder={t("addressLabelPlaceholder")}
            {...form.getInputProps("label")}
          />
          <TextInput label={t("addressLine1")} withAsterisk data-autofocus {...form.getInputProps("line1")} />
          <TextInput label={t("addressLine2")} {...form.getInputProps("line2")} />
          <Group grow>
            <TextInput
              label={t("addressPostalCode")}
              description={form.values.country === "no" ? t("norwegianPostalCodeHint") : undefined}
              {...form.getInputProps("postalCode")}
            />
            <TextInput label={t("addressCity")} {...form.getInputProps("city")} />
          </Group>
          <TextInput label={t("addressRegion")} {...form.getInputProps("region")} />
          <CountrySelect
            locale={locale}
            label={t("countryColumn")}
            withAsterisk
            allowDeselect={false}
            value={form.values.country}
            onChange={(value) => form.setFieldValue("country", value ?? "no")}
            error={form.errors.country}
          />
          <Checkbox
            label={t("primaryAddressCheckbox", { type: addressTypeLabel(t, form.values.type).toLocaleLowerCase() })}
            description={forcedPrimary ? t("primaryAddressHint") : undefined}
            error={form.errors.isPrimary}
            checked={forcedPrimary || form.values.isPrimary}
            disabled={forcedPrimary}
            onChange={(event) => form.setFieldValue("isPrimary", event.currentTarget.checked)}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? t("saveChanges") : t("addAddress")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
