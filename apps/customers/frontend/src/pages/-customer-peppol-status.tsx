import { Alert, Button, Group, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconCircleCheck } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  ApiConflictError,
  ApiValidationError,
  type CustomerBillingProfile,
  type CustomerPeppolLookup,
  checkPeppol,
  customerBillingProfileQueryOptions,
  updateBillingProfile,
} from "../api/billing-profile";
import { invalidateCustomersExcept, syncCustomerRevision } from "../api/customers";
import { useCustomerReload } from "../lib/customer-reload";
import { toInput, valuesFromProfile } from "./-customer-billing-modal";
import "../i18n";

/**
 * Once the server answers 503 (Peppol lookup switched off on this
 * installation — design D5), asking again this session would just cost
 * another round trip for the same no. Module-level rather than component
 * state, on purpose: the setting this reflects belongs to the installation,
 * not to any one customer, so a freshly mounted card (navigating to a
 * different customer, say) must not offer the action back either. A full
 * page reload — a new session — asks again once.
 */
let peppolLookupDisabledForSession = false;

/** The last stored answer in words, with its date already formatted by the caller (design D6's controller ruling). */
const answerMessage = (
  t: (key: string, options?: Record<string, unknown>) => string,
  lookup: CustomerPeppolLookup,
  date: string,
): string => {
  if (lookup.status === "registered") {
    return lookup.canReceiveInvoice
      ? t("peppolCheckedRegisteredInvoice", { date })
      : t("peppolCheckedRegisteredNoInvoice", { date });
  }
  // `no_identifier` is never stored (design D3), so a `peppolLookup` read
  // off the profile is always "registered" or "not_registered" — this is
  // reachable only if that ever changes, and simply says the least it can.
  return t("peppolCheckedNotRegistered", { date });
};

/**
 * The Billing card's Peppol/EHF block (design D3, D4, D6), rendered under
 * the Peppol ID row: the last stored answer in words with its date, a Check
 * EHF action that asks the network again, and — when the last answer says
 * the customer can receive EHF invoices but delivery is not `ehf` yet — the
 * `ehf_available` offer with its own Use EHF action. Split out of
 * `-customer-billing-card.tsx` to keep that file under the repo's
 * line-count guidance. `profile` is the same one the card already fetched;
 * this component reads no query of its own for it, so every mutation below
 * that changes it goes through the query cache (`setQueryData` or
 * `invalidateQueries`) rather than local state, and the card's own
 * `useQuery` is what carries the update back down here as a fresh prop.
 */
export const CustomerPeppolStatus = ({
  customerId,
  profile,
  canManageBilling,
}: {
  customerId: number;
  profile: CustomerBillingProfile;
  canManageBilling?: boolean;
}) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const billingProfileKey = customerBillingProfileQueryOptions(customerId).queryKey;

  const [disabled, setDisabled] = useState(peppolLookupDisabledForSession);
  const [networkError, setNetworkError] = useState(false);
  // `no_identifier` stores nothing server-side (design D3), so the profile's
  // own `peppolLookup` never reflects it — the only place that answer is
  // ever seen is this mutation's own 200 body, held here until the next
  // attempt.
  const [noIdentifier, setNoIdentifier] = useState(false);

  const checkMutation = useMutation({
    mutationFn: () => checkPeppol(customerId),
    onMutate: () => {
      setNetworkError(false);
      setNoIdentifier(false);
    },
    onSuccess: (result) => {
      if (result.status === "no_identifier") {
        setNoIdentifier(true);
        return;
      }
      // The stored answer and the warnings it feeds both live on the GET —
      // simplest and correct to ask for it again rather than reconstruct
      // either from this POST's own body.
      queryClient.invalidateQueries({ queryKey: billingProfileKey });
    },
    onError: (error) => {
      const status = (error as { status?: number }).status;
      if (status === 503) {
        peppolLookupDisabledForSession = true;
        setDisabled(true);
        return;
      }
      if (status === 502) {
        setNetworkError(true);
        return;
      }
      notifications.show({
        color: "red",
        title: t("peppolCheckFailed"),
        message: (error as Error).message,
      });
    },
  });

  const [conflict, setConflict] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const reload = useCustomerReload({
    customerId,
    queryKey: billingProfileKey,
    fetchFresh: () => queryClient.fetchQuery({ ...customerBillingProfileQueryOptions(customerId), staleTime: 0 }),
    revisionOf: (fresh) => fresh.revision,
    seed: () => setConflict(false),
  });

  const useEhfMutation = useMutation({
    mutationFn: () =>
      updateBillingProfile(customerId, {
        ...toInput(valuesFromProfile(profile), profile.revision),
        invoiceDelivery: "ehf",
      }),
    onMutate: () => {
      setConflict(false);
      setFieldError(null);
    },
    onSuccess: (saved) => {
      // Same pattern as `CustomerBillingModal`'s own save: the 200 body
      // goes straight into this query's cache (so the offer's disappearance
      // needs no round trip), the row's fresh revision is carried to the
      // customer query, and the rest of ["customers"] is invalidated since
      // the row moved — but not this key, which already holds what the
      // server just answered.
      queryClient.setQueryData(billingProfileKey, saved);
      syncCustomerRevision(queryClient, customerId, saved.revision);
      invalidateCustomersExcept(queryClient, billingProfileKey);
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        setFieldError(Object.values(error.fieldErrors)[0] ?? error.message);
        return;
      }
      if (error instanceof ApiConflictError && !error.code) {
        // Design D5's revision conflict, same wording as the billing modal's own.
        setConflict(true);
        return;
      }
      notifications.show({ color: "red", title: t("peppolUseEhfFailed"), message: error.message });
    },
  });

  const lookup = profile.peppolLookup;
  const ehfAvailable = profile.warnings.includes("ehf_available");

  return (
    <Stack gap={4}>
      {lookup && (
        <Stack gap={2}>
          <Text size="xs" c="dimmed">
            {answerMessage(t, lookup, formatters.formatDate(lookup.checkedAt))}
          </Text>
          {lookup.smpHost && (
            <Text size="xs" c="dimmed">
              {t("peppolViaHost", { host: lookup.smpHost })}
            </Text>
          )}
        </Stack>
      )}

      {canManageBilling && !disabled && (
        <Group gap="xs">
          <Button size="xs" variant="light" loading={checkMutation.isPending} onClick={() => checkMutation.mutate()}>
            {t("peppolCheckAction")}
          </Button>
        </Group>
      )}
      {disabled && (
        <Text size="xs" c="dimmed">
          {t("peppolLookupDisabled")}
        </Text>
      )}
      {noIdentifier && (
        <Text size="xs" c="dimmed">
          {t("peppolNoIdentifier")}
        </Text>
      )}
      {networkError && (
        <Text size="xs" c="red">
          {t("peppolNetworkError")}
        </Text>
      )}

      {ehfAvailable && (
        <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t("peppolAvailableTitle")}>
          <Stack gap="xs">
            {conflict && (
              <Stack gap={4}>
                <Text size="sm">{t("customerChangedMessage")}</Text>
                <Text size="sm">{t("customerChangesNotSaved")}</Text>
                {reload.failed && (
                  <Text size="sm" c="red">
                    {t("couldNotReload")}
                  </Text>
                )}
                <Group justify="flex-end">
                  <Button size="xs" variant="light" color="yellow" loading={reload.reloading} onClick={reload.reload}>
                    {t("reload")}
                  </Button>
                </Group>
              </Stack>
            )}
            {fieldError && (
              <Text size="sm" c="red">
                {fieldError}
              </Text>
            )}
            {canManageBilling && (
              <Group justify="flex-end">
                <Button size="xs" loading={useEhfMutation.isPending} onClick={() => useEhfMutation.mutate()}>
                  {t("peppolUseEhfAction")}
                </Button>
              </Group>
            )}
          </Stack>
        </Alert>
      )}
    </Stack>
  );
};
