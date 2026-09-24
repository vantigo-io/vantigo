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
import { EHF_AVAILABLE_CODE } from "../lib/billing-labels";
import { useCustomerReload } from "../lib/customer-reload";
import { customerWriteErrorMessage } from "../lib/customer-write-error";
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

/** Test seam only: the flag above deliberately outlives a mount, so a test file resets it between mounts. */
// eslint-disable-next-line react-refresh/only-export-components
export const resetPeppolLookupDisabledForSession = () => {
  peppolLookupDisabledForSession = false;
};

/**
 * The last stored answer in words, with its date already formatted by the
 * caller (design D6's controller ruling) — or nothing at all for a status
 * this version has no words for. `no_identifier` is never stored (design D3),
 * so a `peppolLookup` read off the profile is always "registered" or
 * "not_registered"; should that ever change, saying nothing is the only
 * honest option, since the one thing this must never do is claim "not
 * registered" about an answer it did not understand.
 */
const answerMessage = (
  t: (key: string, options?: Record<string, unknown>) => string,
  lookup: CustomerPeppolLookup,
  date: string,
): string | null => {
  if (lookup.status === "registered") {
    return lookup.canReceiveInvoice
      ? t("peppolCheckedRegisteredInvoice", { date })
      : t("peppolCheckedRegisteredNoInvoice", { date });
  }
  if (lookup.status === "not_registered") return t("peppolCheckedNotRegistered", { date });
  return null;
};

/**
 * The Peppol/EHF answer as it sits in the Billing card's Peppol ID row
 * (design D3, D4, D6): the last stored answer in words with its date and
 * time, which identifier it was about and through which SMP, a Check EHF
 * action that asks the network again, and the notes an answer that stored
 * nothing leaves behind. It lives in that row's right-hand side because it is
 * about that row's value — the participant the server would look up. The
 * `ehf_available` offer the answer can produce is `CustomerEhfOffer` below,
 * rendered at the top of the card instead.
 *
 * Split out of `-customer-billing-card.tsx` to keep that file under the
 * repo's line-count guidance. `profile` is the same one the card already
 * fetched; this component reads no query of its own for it, so every mutation
 * below that changes it goes through the query cache (`setQueryData` or
 * `invalidateQueries`) rather than local state, and the card's own `useQuery`
 * is what carries the update back down here as a fresh prop.
 */
export const CustomerPeppolStatus = ({
  customerId,
  profile,
  identityId,
  canManageBilling,
}: {
  customerId: number;
  profile: CustomerBillingProfile;
  /** The customer's legal identity id, from which the server derives a participant when the profile names none (design D3) — null when unset or withheld. */
  identityId: string | null;
  canManageBilling?: boolean;
}) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const billingProfileKey = customerBillingProfileQueryOptions(customerId).queryKey;

  const [disabled, setDisabled] = useState(peppolLookupDisabledForSession);
  // The two answers that store nothing server-side (design D3): a
  // `no_identifier` and a failed lookup are only ever seen in this mutation's
  // own reply, so they are held here — but *against the participant they were
  // about*. Fill the Peppol ID in and "nothing to look up" is no longer true;
  // a note that outlived its reason would contradict the row right above it.
  const [note, setNote] = useState<{ kind: "no_identifier" | "network_error"; participant: string } | null>(null);
  // What the server would look up right now: the explicit id if there is one,
  // else whatever it would derive from the legal identity.
  const participant = `${profile.peppolId ?? ""}|${identityId ?? ""}`;

  const checkMutation = useMutation({
    mutationFn: () => checkPeppol(customerId),
    onMutate: () => setNote(null),
    onSuccess: (result) => {
      if (result.status === "no_identifier") {
        setNote({ kind: "no_identifier", participant });
        return;
      }
      // The stored answer and the warnings it feeds both live on the GET —
      // simplest and correct to ask for it again rather than reconstruct
      // either from this POST's own body. Returned, not fired and forgotten,
      // so the action stays pending until the fresh answer is on screen.
      const refreshed = queryClient.invalidateQueries({ queryKey: billingProfileKey });
      const previous = profile.peppolLookup;
      // The server records a `customer.peppol_lookup` event only when the
      // answer moved (design D3), and the timeline is on this same page — so
      // when it moved, that query is stale too. Never `["customers", id]`
      // itself: a lookup does not touch the customer row, so its revision and
      // everything keyed on it are still good.
      const changed =
        !previous ||
        previous.status !== result.status ||
        previous.canReceiveInvoice !== result.canReceiveInvoice ||
        previous.canReceiveCreditNote !== result.canReceiveCreditNote;
      if (!changed) return refreshed;
      return Promise.all([
        refreshed,
        queryClient.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] }),
      ]);
    },
    onError: (error) => {
      const status = (error as { status?: number }).status;
      if (status === 503) {
        peppolLookupDisabledForSession = true;
        setDisabled(true);
        return;
      }
      if (status === 502) {
        setNote({ kind: "network_error", participant });
        return;
      }
      notifications.show({
        color: "red",
        title: t("peppolCheckFailed"),
        message: (error as Error).message,
      });
    },
  });

  const lookup = profile.peppolLookup;
  // Date *and* time: two checks on the same day would otherwise read as the
  // same answer, and the whole point of the action is to see it move.
  const answer = lookup
    ? answerMessage(t, lookup, formatters.formatDate(lookup.checkedAt, { dateStyle: "medium", timeStyle: "short" }))
    : null;
  const showAction = canManageBilling && !disabled;
  // A note only stands while the participant it was about is still the one
  // that would be looked up.
  const visibleNote = note?.participant === participant ? note.kind : null;
  const hasNote = disabled || visibleNote !== null;

  // Never checked, nothing to say and nothing to click: no empty stack under
  // the row's own value and hint.
  if (!answer && !lookup?.participantId && !lookup?.smpHost && !showAction && !hasNote) return null;

  return (
    <Stack gap={2} align="flex-end">
      {answer && (
        <Text size="xs" c="dimmed">
          {answer}
        </Text>
      )}
      {/* Which identifier the answer is actually about — withheld from a
          caller without `customers:legal-identity-view` when it was derived
          (design D3), and then simply not said. */}
      {lookup?.participantId && (
        <Text size="xs" c="dimmed">
          {t("peppolLookedUp", { id: lookup.participantId })}
        </Text>
      )}
      {lookup?.smpHost && (
        <Text size="xs" c="dimmed">
          {t("peppolViaHost", { host: lookup.smpHost })}
        </Text>
      )}
      {showAction && (
        <Button size="xs" variant="light" loading={checkMutation.isPending} onClick={() => checkMutation.mutate()}>
          {t("peppolCheckAction")}
        </Button>
      )}
      {/* The outcomes that store nothing, so nothing above them changes: a
          live region, mounted with the action rather than only once it has
          something in it, so a click's result is announced and not merely
          drawn. Without the action there is nothing that could fill it. */}
      {(showAction || hasNote) && (
        <Stack gap={2} align="flex-end" role="status">
          {disabled && (
            <Text size="xs" c="dimmed">
              {t("peppolLookupDisabled")}
            </Text>
          )}
          {visibleNote === "no_identifier" && (
            <Text size="xs" c="dimmed">
              {t("peppolNoIdentifier")}
            </Text>
          )}
          {visibleNote === "network_error" && (
            <Text size="xs" c="red">
              {t("peppolNetworkError")}
            </Text>
          )}
        </Stack>
      )}
    </Stack>
  );
};

/**
 * The `ehf_available` offer (design D4): the last Peppol answer says this
 * customer can receive EHF invoices and delivery is not `ehf` yet. Not a
 * problem but an offer, so it is teal and carries its own Use EHF action —
 * and it belongs at the top of the Billing card beside the yellow warnings,
 * where the card's other card-wide statements are, rather than inside the
 * Peppol ID row that only holds the answer behind it. The 409 that Use EHF
 * can hit is delivery A's own surface, unchanged: a yellow "Customer changed"
 * alert with a Reload, not a red line inside the teal offer.
 */
export const CustomerEhfOffer = ({
  customerId,
  profile,
  canManageBilling,
}: {
  customerId: number;
  profile: CustomerBillingProfile;
  canManageBilling?: boolean;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const billingProfileKey = customerBillingProfileQueryOptions(customerId).queryKey;

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
      // A reload that failed belonged to the *previous* conflict; the modals
      // forget theirs as they open, and this offer's own moment of opening is
      // the click that can conflict again.
      reload.forget();
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
      notifications.show({
        color: "red",
        title: t("peppolUseEhfFailed"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });

  if (!profile.warnings.includes(EHF_AVAILABLE_CODE)) return null;

  return (
    <>
      {conflict && (
        <Alert color="yellow" title={t("customerChangedTitle")}>
          <Stack gap="xs">
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
        </Alert>
      )}
      <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t("peppolAvailableTitle")}>
        <Stack gap="xs">
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
    </>
  );
};
