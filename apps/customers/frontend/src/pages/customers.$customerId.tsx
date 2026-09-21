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
import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
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
  updateCustomer,
} from "../api/customers";
import {
  CopyableBadge,
  LegalCountryBadge,
  LegalSourceBadge,
  LegalTypeBadge,
  LegalValueBadge,
} from "../components/legal-badges";
import { getLegalSource } from "../lib/legal-sources";
import { CustomerContactsCard } from "./-customer-contacts-card";
import { CustomerFormModal, type CustomerModalState } from "./-customer-form-modal";
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
    onSuccess: () => {
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
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({
        color: "teal",
        title: t("customerRestored"),
        message: t("customerRestoredMessage", { name: customer.name }),
      });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("customerCouldNotBeRestored"), message: error.message });
    },
  });
};

export const CustomerOverview = ({ customerId }: { customerId: number }) => (
  <Stack gap="lg">
    <CustomerContactsCard customerId={customerId} />
    <CustomerTimeline customerId={customerId} />
  </Stack>
);
