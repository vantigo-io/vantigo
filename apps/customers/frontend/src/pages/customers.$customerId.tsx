import { Alert, Badge, Button, Group, List, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconArchive,
  IconArrowBackUp,
  IconArrowsExchange,
  IconBuilding,
  IconPencil,
  IconUser,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";

import {
  ApiConflictError,
  archiveCustomer,
  type CustomerResponse,
  type CustomerType,
  changeCustomerType,
  customerQueryOptions,
  legalIdentityQueryOptions,
  syncCustomerRevision,
  updateCustomer,
} from "../api/customers";
import { customerRegistryRecordQueryOptions } from "../api/registry";
import {
  CopyableBadge,
  LegalCountryBadge,
  LegalSourceBadge,
  LegalTypeBadge,
  LegalValueBadge,
} from "../components/legal-badges";
import { getLegalSource } from "../lib/legal-sources";
import { CustomerBillingCard } from "./-customer-billing-card";
import { CustomerContactCard } from "./-customer-contact-card";
import { CustomerContactsCard } from "./-customer-contacts-card";
import { CustomerFormModal, type CustomerModalState } from "./-customer-form-modal";
import { CustomerRegistryCard } from "./-customer-registry-card";
import { CustomerRelationshipCard } from "./-customer-relationship-card";
import { CustomerTimeline } from "./-customer-timeline";
import "../i18n";

/**
 * The customer page's header. `actions` lets the host append entries that
 * lead out of the page (the Communications inbox, say) next to the
 * customer's own actions, without this package knowing about other modules.
 * `canArchive`/`canRestore` come from the host, which reads the caller's
 * `customers:delete`/`customers:update` permissions — this package never
 * fetches permissions itself.
 */
export const CustomerDetailHeader = ({
  customerId,
  actions,
  canArchive,
  canRestore,
}: {
  customerId: number;
  actions?: ReactNode;
  canArchive?: boolean;
  canRestore?: boolean;
}) => {
  const { t, formatters } = useI18n("customers");
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const [modalState, setModalState] = useState<CustomerModalState | null>(null);
  const confirmTypeChange = useCustomerTypeChange(customer);
  const confirmArchive = useArchiveCustomer(customer);
  const restore = useRestoreCustomer(customer);
  const isArchived = customer.status === "archived";

  const { data: identity } = useSuspenseQuery(legalIdentityQueryOptions(customerId));
  const TypeIcon = customer.type === "business" ? IconBuilding : IconUser;

  return (
    <Stack gap="lg">
      {isArchived && (
        <Alert color="gray" icon={<IconArchive size={16} />} title={t("archivedBannerTitle")}>
          {t("archivedBannerMessage")}
        </Alert>
      )}
      <Stack gap="xs">
        <PageHeader
          breadcrumbs={[{ label: t("customers"), to: "/customers" }, { label: customer.name }]}
          title={customer.name}
          description={
            identity ? (
              <Group gap="xs" mt={4}>
                <LegalValueBadge type={identity.type} tooltip={t("tooltipLegalName")} copyable>
                  {identity.name}
                </LegalValueBadge>
                <LegalValueBadge type={identity.type} tooltip={t("tooltipLegalId")} copyable>
                  {identity.id}
                </LegalValueBadge>
                <LegalCountryBadge country={identity.country} tooltip={t("tooltipCountry")} copyable />
                <LegalTypeBadge type={identity.type} tooltip={t("tooltipType")} copyable />
                <LegalSourceBadge
                  source={identity.source}
                  legalId={identity.id}
                  tooltip={getLegalSource(identity.source).logo ? undefined : t("tooltipManualSource")}
                />
              </Group>
            ) : (
              t("legalIdentityUnavailable")
            )
          }
          actions={
            <Group gap="sm">
              <Badge variant="light" size="lg" color={customer.status === "active" ? "teal" : "gray"}>
                {customer.status === "active"
                  ? t("statusActive")
                  : customer.status === "archived"
                    ? t("statusArchived")
                    : t("statusDisabled")}
              </Badge>
              <Badge
                variant="light"
                size="lg"
                color={customer.type === "business" ? "indigo" : "grape"}
                leftSection={<TypeIcon size={12} />}
              >
                {customerTypeLabel(t, customer.type)}
              </Badge>
              <CopyableBadge variant="light" size="lg" tooltip={t("customerIdTooltip")} copyValue={String(customer.id)}>
                #{customer.id}
              </CopyableBadge>
              <Button
                variant="default"
                leftSection={<IconPencil size={16} />}
                onClick={() => setModalState({ mode: "edit", customer })}
              >
                {t("editCustomer")}
              </Button>
              <Button
                variant="subtle"
                color="gray"
                leftSection={<IconArrowsExchange size={16} />}
                onClick={confirmTypeChange}
              >
                {t("changeCustomerType")}
              </Button>
              {canArchive && !isArchived && (
                <Button variant="light" color="red" leftSection={<IconArchive size={16} />} onClick={confirmArchive}>
                  {t("archiveCustomer")}
                </Button>
              )}
              {canRestore && isArchived && (
                <Button
                  variant="light"
                  color="teal"
                  leftSection={<IconArrowBackUp size={16} />}
                  loading={restore.isPending}
                  onClick={() => restore.mutate()}
                >
                  {t("restoreCustomer")}
                </Button>
              )}
              {actions}
            </Group>
          }
        />
        <Text size="sm" c="dimmed">
          {t("createdOnDate", { date: formatters.formatDate(customer.createdAt) })}
          {" · "}
          {t("updatedOnDate", { date: formatters.formatDate(customer.updatedAt) })}
        </Text>
      </Stack>

      <CustomerFormModal state={modalState} onClose={() => setModalState(null)} />
    </Stack>
  );
};

const customerTypeLabel = (t: (key: string) => string, type: CustomerType) =>
  type === "business" ? t("customerTypeBusiness") : t("customerTypePerson");

/**
 * The explicit way to change a customer's type once it exists. The form modal
 * never offers it: the change is rarely right for a customer with history, so
 * it goes through the shared confirm modal, which spells out what it does to
 * the legal identity, before calling the dedicated endpoint.
 */
const useCustomerTypeChange = (customer: CustomerResponse) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const target: CustomerType = customer.type === "business" ? "person" : "business";
  const from = customerTypeLabel(t, customer.type).toLocaleLowerCase();
  const to = customerTypeLabel(t, target).toLocaleLowerCase();
  const mutation = useMutation({
    mutationFn: () => changeCustomerType(customer.id, target, customer.revision),
    onSuccess: (changed) => {
      // The 200 body carries the row's new revision: hand it to every cache
      // entry built on it before the refetch below lands, so an editor
      // opened in that window does not send the one this write replaced.
      syncCustomerRevision(queryClient, customer.id, changed.revision);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({
        color: "teal",
        title: t("customerTypeChanged"),
        message: t("customerTypeChangedMessage", { name: customer.name, to }),
      });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        // D5's revision conflict: nothing to fix but look at the latest
        // version, so the customer query is refetched rather than shown a
        // field-level error the type-change dialog has no field for. The
        // broad ["customers"] prefix (as onSuccess above also invalidates)
        // matches this detail query regardless of whether the route's
        // customerId param arrives as a number or a string.
        queryClient.invalidateQueries({ queryKey: ["customers"] });
        notifications.show({ color: "yellow", title: t("customerChangedTitle"), message: t("customerChangedMessage") });
        return;
      }
      notifications.show({ color: "red", title: t("customerTypeCouldNotBeChanged"), message: error.message });
    },
  });

  return () =>
    modals.openConfirmModal({
      title: t("changeCustomerTypeTitle"),
      children: (
        <Stack gap="sm">
          <Text size="sm">{t("changeCustomerTypeIntro", { name: customer.name, from, to })}</Text>
          <List size="sm" spacing="xs">
            <List.Item>{t("changeCustomerTypeKeeps")}</List.Item>
            <List.Item>{t("changeCustomerTypeRemovesIdentity")}</List.Item>
            <List.Item>{t("changeCustomerTypeAffectsForms", { to })}</List.Item>
          </List>
          <Text size="sm" fw={600}>
            {t("changeCustomerTypeRarely")}
          </Text>
        </Stack>
      ),
      labels: { confirm: t("changeCustomerTypeConfirm", { to }), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => mutation.mutate(),
    });
};

/**
 * Archive (design D7): kept, but hidden from most lists until restored — the
 * shared confirm-modal pattern, since it is the one destructive-looking
 * action a customer offers (see `useCustomerTypeChange` above).
 */
const useArchiveCustomer = (customer: CustomerResponse) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => archiveCustomer(customer.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({
        color: "teal",
        title: t("customerArchived"),
        message: t("customerArchivedMessage", { name: customer.name }),
      });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("customerCouldNotBeArchived"), message: error.message });
    },
  });

  return () =>
    modals.openConfirmModal({
      title: t("archiveCustomerTitle"),
      children: <Text size="sm">{t("archiveCustomerConfirm", { name: customer.name })}</Text>,
      labels: { confirm: t("archiveCustomer"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => mutation.mutate(),
    });
};

/**
 * Restore (design D7): a PUT with `status: "active"`, same as any other
 * update — unlike Archive it undoes nothing destructive, so it goes straight
 * through without a confirmation step.
 */
const useRestoreCustomer = (customer: CustomerResponse) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      updateCustomer(customer.id, { name: customer.name, status: "active", revision: customer.revision }),
    onSuccess: (restored) => {
      // Same as the type change above: the fresh revision travels with the
      // response, not a round trip later.
      syncCustomerRevision(queryClient, customer.id, restored.revision);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({
        color: "teal",
        title: t("customerRestored"),
        message: t("customerRestoredMessage", { name: customer.name }),
      });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        // D5's revision conflict, handled exactly as useCustomerTypeChange
        // handles its own: Restore sends the revision this page was rendered
        // with, so it can lose the race too, and a red "could not be restored"
        // would leave the caller pressing a button that keeps failing on the
        // same stale revision. A code, by contrast, means the duplicate-identity
        // conflict, which Restore cannot raise — it sends no identity.
        queryClient.invalidateQueries({ queryKey: ["customers"] });
        notifications.show({ color: "yellow", title: t("customerChangedTitle"), message: t("customerChangedMessage") });
        return;
      }
      notifications.show({ color: "red", title: t("customerCouldNotBeRestored"), message: error.message });
    },
  });
};

/**
 * `canManageBilling` (design D6) comes from the host's own
 * `customers:billing-manage` check, deliberately separate from `canEdit`
 * (`customers:update`) — see `-customer-billing-card.tsx`. The customer
 * itself is fetched once here and passed to `CustomerBillingCard`, which
 * needs its `contactInfo`, `type` and `identity` for the resolved-recipient
 * hints, rather than that card issuing a query of its own.
 *
 * `canViewIdentity`/`canManageIdentity` (Brreg in full design D5) are the
 * host's `customers:legal-identity-view`/`-manage` checks, the two doors the
 * Registry card sits behind. The registry record is fetched here rather than
 * inside that card because the addresses section below offers the registry's
 * own addresses (design D3) and both read the one query.
 *
 * `CustomerRelationshipCard` (owner and tags design D3) is first in the
 * stack: who owns the relationship and how the customer is classified are
 * what a person looks for before the contact details. It is its own card
 * rather than folded into "Contact & addresses" — the spec's owner and tags
 * pairing is a distinct concern from a customer's own contact info.
 *
 * `canManageTimeline` (follow-ups design D3) is the host's
 * `customers:timeline-manage` check, and it is the first capability here that
 * closes a gap rather than adding one: the timeline card's Add, Edit and Delete
 * controls were always server-enforced, so a reader saw buttons that answered
 * 403. It also gates the new Done/Reopen control on a follow-up.
 */
export const CustomerOverview = ({
  customerId,
  canEdit,
  canManageBilling,
  canViewIdentity,
  canManageIdentity,
  canManageTimeline,
}: {
  customerId: number;
  canEdit?: boolean;
  canManageBilling?: boolean;
  canViewIdentity?: boolean;
  canManageIdentity?: boolean;
  canManageTimeline?: boolean;
}) => {
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  // Enhetsregisteret answers for Norwegian businesses and nothing else, and
  // the record repeats the legal identity's organisation number — so for any
  // other customer, or any caller without that permission, the record is not
  // even asked for: the GET would answer 204 whatever the reason. The
  // identity's own `type` is checked too, not just the customer's: the two
  // can disagree (a customer mid change-of-type, say), and it is the identity
  // whose organisation number this record would repeat.
  const showRegistry = Boolean(
    canViewIdentity &&
      customer.type === "business" &&
      customer.identity?.type === "business" &&
      customer.identity?.country === "no",
  );
  const { data: registryRecord } = useQuery({
    ...customerRegistryRecordQueryOptions(customerId),
    enabled: showRegistry,
  });
  return (
    <Stack gap="lg">
      <CustomerRelationshipCard customerId={customerId} canEdit={canEdit} />
      <CustomerContactCard customerId={customerId} canEdit={canEdit} registryRecord={registryRecord ?? null} />
      {showRegistry && <CustomerRegistryCard customerId={customerId} canManageIdentity={canManageIdentity} />}
      <CustomerBillingCard customerId={customerId} customer={customer} canManageBilling={canManageBilling} />
      <CustomerContactsCard customerId={customerId} />
      <CustomerTimeline customerId={customerId} canManageTimeline={canManageTimeline} />
    </Stack>
  );
};
