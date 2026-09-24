import { Alert, Button, Group, List, Modal, Stack, Text } from "@mantine/core";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  ApiConflictError,
  type CustomerResponse,
  type CustomerType,
  customerQueryOptions,
  invalidateCustomersExcept,
  syncCustomerRevision,
} from "../api/customers";
import { type CustomerMergeMove, type CustomerMergeResult, mergeCustomer } from "../api/merge";
import { CustomerPicker } from "../components/customer-picker";
import { CUSTOMER_ANONYMISED_CODE, CUSTOMER_MERGED_CODE } from "../lib/customer-write-error";
import "../i18n";

/** The words for each kind a merge can move (merge design D3); a kind missing here still shows, as `mergeMovedOther`. */
const moveKeys: Record<string, string> = {
  "customers.contacts": "mergeMovedContacts",
  "customers.addresses": "mergeMovedAddresses",
  "customers.timelineEntries": "mergeMovedTimelineEntries",
  "customers.tags": "mergeMovedTags",
  "projects.projects": "mergeMovedProjects",
  "energy.supplyPeriods": "mergeMovedSupplyPeriods",
  "communications.conversations": "mergeMovedConversations",
  "communications.conversationSuggestions": "mergeMovedConversationSuggestions",
  "communications.conversationCandidates": "mergeMovedConversationCandidates",
};

/** The server's refusals by code (merge design D2); anything else shows the server's own detail. */
const refusalKeys: Record<string, string> = {
  merge_self: "mergeRefusedSelf",
  merge_type_mismatch: "mergeRefusedTypeMismatch",
  merge_into_archived: "mergeRefusedIntoArchived",
  merge_already_merged: "mergeRefusedAlreadyMerged",
  // This customer itself was merged away from another tab: every
  // customer-scoped write answers it so (see lib/customer-write-error).
  [CUSTOMER_MERGED_CODE]: "customerMergedMessage",
  // The picked duplicate was anonymised (GDPR design D4): nothing of a person
  // is left in it to fold in.
  [CUSTOMER_ANONYMISED_CODE]: "customerAnonymisedMessage",
};

const typePhraseKey = (type: CustomerType) => (type === "person" ? "mergeTypePerson" : "mergeTypeBusiness");

/**
 * Merge… (customers merge design D4): `customer` absorbs the one picked here.
 * Everything the server would refuse that the page can already see — two
 * types, an archived survivor — is said before the button, and the button is
 * off; what only the server knows (the pick merged away meanwhile, a stale
 * revision) comes back as its 409 and is said in words. The survivor's
 * revision is the one the page holds; a 409 without a code is that revision
 * going stale, answered the way the header's other writes answer it — the
 * page refetches, so the next Merge sends the fresh revision and just works.
 * A coded refusal refetches too: the picker's cached list may still offer a
 * customer merged away meanwhile, and the survivor itself may have been
 * archived or merged away from another tab.
 *
 * On success the answered survivor goes into the cache and its revision is
 * synced before the broad invalidation — the page refreshes, the timeline
 * shows customer.merged — and the modal says what moved, zeros left out.
 */
export const CustomerMergeModal = ({
  customer,
  opened,
  onClose,
}: {
  customer: CustomerResponse;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const [source, setSource] = useState<CustomerResponse | null>(null);
  const [done, setDone] = useState<{ source: CustomerResponse; result: CustomerMergeResult } | null>(null);
  const [refusal, setRefusal] = useState<{ stale: boolean; message: string } | null>(null);
  const mutation = useMutation({
    mutationFn: (picked: CustomerResponse) =>
      mergeCustomer(customer.id, { sourceId: picked.id, revision: customer.revision }),
    onSuccess: (result, picked) => {
      // The answer is the survivor as GET would give it, so it goes straight
      // into the cache and its revision to every entry holding one; the rest of
      // ["customers"] — contacts, addresses, timeline, lists, the absorbed
      // customer — is invalidated, since all of it moved.
      const customerKey = customerQueryOptions(customer.id).queryKey;
      queryClient.setQueryData(customerKey, result.customer);
      syncCustomerRevision(queryClient, customer.id, result.customer.revision);
      invalidateCustomersExcept(queryClient, customerKey);
      setDone({ source: picked, result });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        queryClient.invalidateQueries({ queryKey: ["customers"] });
        setRefusal({ stale: true, message: t("mergeCustomerChangedMessage") });
        return;
      }
      if (error instanceof ApiConflictError) {
        queryClient.invalidateQueries({ queryKey: ["customers"] });
        // The pick was merged away meanwhile: it is not one to offer again.
        if (error.code === "merge_already_merged") setSource(null);
      }
      const key = error instanceof ApiConflictError && error.code ? refusalKeys[error.code] : undefined;
      setRefusal({ stale: false, message: key ? t(key) : error.message });
    },
  });
  // The component stays mounted while closed, so a pick, a refusal or a result
  // left behind would greet the next opening.
  const close = () => {
    setSource(null);
    setDone(null);
    setRefusal(null);
    mutation.reset();
    onClose();
  };
  const typeMismatch = source !== null && source.type !== customer.type;
  const intoArchived = customer.status === "archived";

  return (
    <Modal opened={opened} onClose={close} title={t("mergeCustomerTitle", { name: customer.name })} centered size="lg">
      {done ? (
        <Stack>
          <Text size="sm">{t("mergeDoneMessage", { number: done.source.customerNumber, name: done.source.name })}</Text>
          <MovedList moved={done.result.moved} />
          <Group justify="flex-end">
            <Button onClick={close}>{t("mergeClose")}</Button>
          </Group>
        </Stack>
      ) : (
        <Stack>
          <CustomerPicker
            label={t("mergeSourceLabel")}
            placeholder={t("mergeSourcePlaceholder")}
            excludeId={customer.id}
            value={source}
            onChange={(next) => {
              setSource(next);
              setRefusal(null);
            }}
            disabled={mutation.isPending}
          />
          {source && (
            <>
              <Text size="sm">{t("mergeWhatHappens", { number: source.customerNumber, name: source.name })}</Text>
              <Text size="sm" c="dimmed">
                {t("mergeKeepsOwnDetails")}
              </Text>
            </>
          )}
          {source && typeMismatch && (
            <Alert color="yellow">
              {t("mergeTypeMismatchWarning", {
                number: source.customerNumber,
                name: source.name,
                sourceType: t(typePhraseKey(source.type)),
                targetType: t(typePhraseKey(customer.type)),
              })}
            </Alert>
          )}
          {intoArchived && <Alert color="yellow">{t("mergeIntoArchivedWarning")}</Alert>}
          {refusal && (
            <Alert
              color={refusal.stale ? "yellow" : "red"}
              title={refusal.stale ? t("customerChangedTitle") : t("mergeCouldNotMerge")}
            >
              {refusal.message}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={close}>
              {t("cancel")}
            </Button>
            <Button
              color="red"
              disabled={source === null || typeMismatch || intoArchived}
              loading={mutation.isPending}
              onClick={() => source && mutation.mutate(source)}
            >
              {t("mergeConfirm")}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
};

/** What moved, in the answer's order, zeros left out — "It had nothing else to move." when all were. */
const MovedList = ({ moved }: { moved: CustomerMergeMove[] }) => {
  const { t } = useI18n("customers");
  const something = moved.filter((m) => m.count > 0);
  if (something.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        {t("mergeMovedNothing")}
      </Text>
    );
  }
  return (
    <List size="sm">
      {something.map((m) => (
        <List.Item key={m.kind}>
          {moveKeys[m.kind]
            ? t(moveKeys[m.kind], { count: m.count })
            : t("mergeMovedOther", { count: m.count, kind: m.kind })}
        </List.Item>
      ))}
    </List>
  );
};
