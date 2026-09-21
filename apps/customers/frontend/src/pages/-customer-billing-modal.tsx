import { Alert, Button, Group, Modal, NumberInput, Select, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  ApiConflictError,
  ApiValidationError,
  type CustomerBillingProfile,
  type CustomerBillingProfileInput,
  customerBillingProfileQueryOptions,
  updateBillingProfile,
} from "../api/billing-profile";
import { billingLanguageLabel, deliveryMethodLabel } from "../lib/billing-labels";
import "../i18n";

interface BillingFormValues {
  invoiceEmail: string;
  reminderEmail: string;
  paymentTermsDays: number | string;
  currency: string;
  language: string | null;
  invoiceDelivery: string | null;
  reminderDelivery: string | null;
  peppolId: string;
  gln: string;
  buyerReference: string;
}

const valuesFromProfile = (profile: CustomerBillingProfile): BillingFormValues => ({
  invoiceEmail: profile.invoiceEmail ?? "",
  reminderEmail: profile.reminderEmail ?? "",
  paymentTermsDays: profile.paymentTermsDays ?? "",
  currency: profile.currency ?? "",
  language: profile.language,
  invoiceDelivery: profile.invoiceDelivery,
  reminderDelivery: profile.reminderDelivery,
  peppolId: profile.peppolId ?? "",
  gln: profile.gln ?? "",
  buyerReference: profile.buyerReference ?? "",
});

/** A NumberInput's own value type (`number | string`), empty string meaning "cleared" — mapped to null on submit. */
const numeric = (value: number | string): number | null => {
  if (typeof value === "number") return Number.isFinite(value) ? value : null;
  const trimmed = value.trim();
  if (trimmed === "") return null;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : null;
};

const toInput = (values: BillingFormValues, revision: number): CustomerBillingProfileInput => ({
  invoiceEmail: values.invoiceEmail.trim() || null,
  reminderEmail: values.reminderEmail.trim() || null,
  paymentTermsDays: numeric(values.paymentTermsDays),
  currency: values.currency.trim() || null,
  language: values.language,
  invoiceDelivery: values.invoiceDelivery,
  reminderDelivery: values.reminderDelivery,
  peppolId: values.peppolId.trim() || null,
  gln: values.gln.trim() || null,
  buyerReference: values.buyerReference.trim() || null,
  revision,
});

/**
 * Edits a customer's billing profile (design D1, D4). Always a full replace
 * — there is no create/edit distinction, the same reason
 * `CustomerContactInfoModal` has none. The modal-local `revision` starts at
 * `profile.revision` (the billing profile's own revision *is* the customer
 * row's — design D4's write bumps the same counter contact info and the
 * customer PUT do) and is re-seeded, together with every field, by the
 * Reload button on a 409 — the same pattern `CustomerContactInfoModal` uses,
 * except reloading refetches the billing-profile query, not the customer.
 */
export const CustomerBillingModal = ({
  opened,
  customerId,
  profile,
  onClose,
}: {
  opened: boolean;
  customerId: number;
  profile: CustomerBillingProfile;
  onClose: () => void;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const [conflict, setConflict] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [revision, setRevision] = useState(profile.revision);
  // Re-seeds the form and the revision this modal will send next whenever it
  // is (re)opened — the same open-transition pattern
  // `CustomerContactInfoModal` uses, and for the same reason: a background
  // refetch of the billing profile while the modal is open must not blow
  // away what the caller is mid-typing.
  const [wasOpened, setWasOpened] = useState(opened);
  const form = useForm<BillingFormValues>({ initialValues: valuesFromProfile(profile) });
  if (opened !== wasOpened) {
    setWasOpened(opened);
    if (opened) {
      form.setValues(valuesFromProfile(profile));
      form.resetDirty();
      form.clearErrors();
      setRevision(profile.revision);
      setConflict(false);
    }
  }

  const mutation = useMutation({
    mutationFn: (values: BillingFormValues) => updateBillingProfile(customerId, toInput(values, revision)),
    onSuccess: (saved) => {
      // The row's revision moved — set this query's own data from the 200
      // body (so the card's warnings and hints are fresh without a refetch)
      // and invalidate the `["customers"]` prefix so the customer query and
      // any other form built on its revision do not keep a stale one.
      queryClient.setQueryData(customerBillingProfileQueryOptions(customerId).queryKey, saved);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      setConflict(false);
      onClose();
      notifications.show({ color: "teal", title: t("billingProfileUpdated"), message: t("billingProfileSaved") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      if (error instanceof ApiConflictError && !error.code) {
        // Design D5's revision conflict, same as CustomerContactInfoModal's own.
        setConflict(true);
        return;
      }
      notifications.show({ color: "red", title: t("billingProfileCouldNotBeSaved"), message: error.message });
    },
  });

  const reload = async () => {
    setReloading(true);
    try {
      const fresh = await queryClient.fetchQuery({ ...customerBillingProfileQueryOptions(customerId), staleTime: 0 });
      form.setValues(valuesFromProfile(fresh));
      form.resetDirty();
      form.clearErrors();
      setRevision(fresh.revision);
      setConflict(false);
    } finally {
      setReloading(false);
    }
  };

  return (
    <Modal opened={opened} onClose={onClose} title={t("editBillingProfile")} centered>
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          {conflict && (
            <Alert color="yellow" title={t("customerChangedTitle")}>
              <Stack gap="xs">
                <Text size="sm">{t("customerChangedMessage")}</Text>
                <Text size="sm">{t("customerChangesNotSaved")}</Text>
                <Group justify="flex-end">
                  <Button size="xs" variant="light" color="yellow" loading={reloading} onClick={reload}>
                    {t("reload")}
                  </Button>
                </Group>
              </Stack>
            </Alert>
          )}
          <TextInput
            label={t("billingInvoiceEmail")}
            placeholder={t("emailPlaceholder")}
            data-autofocus
            {...form.getInputProps("invoiceEmail")}
          />
          <TextInput
            label={t("billingReminderEmail")}
            placeholder={t("emailPlaceholder")}
            {...form.getInputProps("reminderEmail")}
          />
          <NumberInput
            label={t("billingPaymentTermsDays")}
            min={0}
            max={365}
            {...form.getInputProps("paymentTermsDays")}
          />
          <TextInput
            label={t("billingCurrency")}
            maxLength={3}
            {...form.getInputProps("currency")}
            onChange={(event) => form.setFieldValue("currency", event.currentTarget.value.toUpperCase())}
          />
          <Select
            label={t("billingLanguage")}
            data={["nb", "en"].map((value) => ({ value, label: billingLanguageLabel(t, value) }))}
            clearable
            {...form.getInputProps("language")}
          />
          <Select
            label={t("billingInvoiceDelivery")}
            data={["email", "ehf", "efaktura", "paper"].map((value) => ({
              value,
              label: deliveryMethodLabel(t, value),
            }))}
            clearable
            {...form.getInputProps("invoiceDelivery")}
          />
          <Select
            label={t("billingReminderDelivery")}
            data={["email", "paper"].map((value) => ({ value, label: deliveryMethodLabel(t, value) }))}
            clearable
            {...form.getInputProps("reminderDelivery")}
          />
          <TextInput
            label={t("billingPeppolId")}
            placeholder={t("billingPeppolIdPlaceholder")}
            {...form.getInputProps("peppolId")}
          />
          <TextInput label={t("billingGln")} {...form.getInputProps("gln")} />
          <TextInput
            label={t("billingBuyerReference")}
            description={t("billingBuyerReferenceHint")}
            {...form.getInputProps("buyerReference")}
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
