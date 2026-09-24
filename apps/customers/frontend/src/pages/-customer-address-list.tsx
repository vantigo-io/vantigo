import { ActionIcon, Badge, Button, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconBuildingBank, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  ADDRESS_TYPE_ORDER,
  type CustomerAddress,
  type CustomerAddressType,
  customerAddressesQueryOptions,
  deleteCustomerAddress,
  makeAddressPrimary,
} from "../api/addresses";
import { customerBillingProfileQueryOptions } from "../api/billing-profile";
import type { CustomerRegistryAddress, CustomerRegistryRecord } from "../api/registry";
import { addressTypeLabel } from "../lib/address-type-label";
import { countryDisplayName } from "../lib/country-display";
import { customerWriteErrorMessage } from "../lib/customer-write-error";
import { registryAddressValues } from "../lib/registry-address";
import { type AddressModalState, CustomerAddressModal } from "./-customer-address-modal";
import "../i18n";

/**
 * The addresses half of the "Contact & addresses" card (design D6): grouped
 * by type in the fixed order invoice/postal/delivery/visiting, primary
 * badged, with add/edit/delete and "Make primary" behind `canEdit`. Not
 * prefetched by the host's route loader — the customer detail route's
 * loader only ensures the customer itself, the way its sibling Overview
 * data (the Contacts card) is not prefetched either — so this loads with a
 * skeleton like that sibling.
 *
 * `registryRecord` is the registry's view of the customer (design D1), handed
 * down by the Overview tab, which already holds that query for the Registry
 * card — null when there is none, or when the viewer may not see it. It is
 * only ever *offered* here (design D3): an address on file may deliberately
 * differ from the register, so nothing is written without a click.
 */
export const CustomerAddressesSection = ({
  customerId,
  canEdit,
  registryRecord,
}: {
  customerId: number;
  canEdit?: boolean;
  registryRecord?: CustomerRegistryRecord | null;
}) => {
  const { t, locale } = useI18n("customers");
  const queryClient = useQueryClient();
  const { data, isPending, isError, refetch } = useQuery(customerAddressesQueryOptions(customerId));
  const addresses = data ?? [];
  const [modalState, setModalState] = useState<AddressModalState>(null);

  // An address write moves no revision on the customer row (design D3), so
  // the customer query is left alone — but the billing profile's warnings
  // are computed from the customer's addresses at read time (design D4):
  // `no_invoice_address` goes the moment an invoice address exists, and
  // comes back when the last one is deleted.
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: customerAddressesQueryOptions(customerId).queryKey });
    queryClient.invalidateQueries({ queryKey: customerBillingProfileQueryOptions(customerId).queryKey });
  };

  const makePrimaryMutation = useMutation({
    mutationFn: (address: CustomerAddress) => makeAddressPrimary(customerId, address),
    onSuccess: () => {
      invalidate();
      notifications.show({ color: "teal", title: t("addressMadePrimary"), message: t("addressMadePrimaryMessage") });
    },
    onError: (error) => {
      notifications.show({
        color: "red",
        title: t("addressCouldNotBeMadePrimary"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });

  const remove = useMutation({
    mutationFn: (address: CustomerAddress) => deleteCustomerAddress(customerId, address.id),
    onSuccess: () => {
      invalidate();
      notifications.show({ color: "teal", title: t("addressDeleted"), message: t("addressDeletedMessage") });
    },
    onError: (error) => {
      notifications.show({
        color: "red",
        title: t("addressCouldNotBeDeleted"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });

  const confirmDelete = (address: CustomerAddress) =>
    modals.openConfirmModal({
      title: t("deleteAddressTitle"),
      children: (
        <Text size="sm">
          {t("deleteAddressConfirm", { type: addressTypeLabel(t, address.type).toLocaleLowerCase() })}
        </Text>
      ),
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(address),
    });

  const addButton = (
    <Button
      variant="light"
      size="xs"
      leftSection={<IconPlus size={14} />}
      onClick={() => setModalState({ mode: "add" })}
    >
      {t("addAddress")}
    </Button>
  );

  const groups = ADDRESS_TYPE_ORDER.map((type) => ({
    type,
    items: addresses.filter((address) => address.type === type),
  })).filter((group) => group.items.length > 0);

  // The registry's business address becomes a visiting address and its postal
  // address a postal one (design D3). An offer stands only while the customer
  // has no address of that type: once it has one, the registry's is either
  // already on file or a deliberate difference, and neither is an invitation
  // to add a second. Withheld while the list is still loading or has failed —
  // an empty `addresses` then says nothing about what the customer has.
  const offers = [
    {
      type: "visiting" as CustomerAddressType,
      label: t("useRegistryBusinessAddress"),
      address: registryRecord?.businessAddress,
    },
    {
      type: "postal" as CustomerAddressType,
      label: t("useRegistryPostalAddress"),
      address: registryRecord?.postalAddress,
    },
  ].filter(
    (offer): offer is { type: CustomerAddressType; label: string; address: CustomerRegistryAddress } =>
      !!offer.address && !addresses.some((address) => address.type === offer.type),
  );
  const showOffers = canEdit && !isPending && !isError && offers.length > 0;

  return (
    <Stack gap="xs">
      <Group justify="space-between">
        <Text fw={500} size="sm" c="dimmed" component="h4">
          {t("addresses")}
        </Text>
        {canEdit && addresses.length > 0 && addButton}
      </Group>

      {isPending ? (
        <ContentSkeleton rows={2} rowHeight={48} />
      ) : isError ? (
        // A failed load is not an empty list: "No addresses yet" would invite
        // adding a second copy of an address the customer already has. Same
        // shape as the timeline's own failure.
        <Stack align="center" py="md">
          <Text c="red">{t("failedLoadAddresses")}</Text>
          <Button variant="light" onClick={() => refetch()}>
            {t("tryAgain")}
          </Button>
        </Stack>
      ) : addresses.length === 0 ? (
        <EmptyState size="sm" title={t("noAddressesYet")} action={canEdit ? addButton : undefined} />
      ) : (
        <Stack gap="md">
          {groups.map((group) => (
            <Stack key={group.type} gap={6}>
              <Text size="xs" fw={600} tt="uppercase" c="dimmed">
                {addressTypeLabel(t, group.type)}
              </Text>
              <Stack gap={4}>
                {group.items.map((address) => {
                  // The visible label is short ("Edit", a pencil icon, "Make
                  // primary"), but a customer with two addresses of the same
                  // type (two "Delivery" addresses, say) would otherwise give
                  // every row's actions the same accessible name — no way for
                  // assistive tech to tell which button acts on which address.
                  // Each aria-label names its own row's address.
                  const addressName = address.label ?? address.line1;
                  return (
                    <Group key={address.id} justify="space-between" align="flex-start" wrap="nowrap">
                      <Stack gap={0}>
                        {address.label && (
                          <Text size="sm" fw={500}>
                            {address.label}
                          </Text>
                        )}
                        <Text size="sm">{address.line1}</Text>
                        {address.line2 && <Text size="sm">{address.line2}</Text>}
                        {(address.postalCode || address.city) && (
                          <Text size="sm" c="dimmed">
                            {[address.postalCode, address.city].filter(Boolean).join(" ")}
                          </Text>
                        )}
                        {address.region && (
                          <Text size="sm" c="dimmed">
                            {address.region}
                          </Text>
                        )}
                        <Text size="xs" c="dimmed">
                          {countryDisplayName(address.country, locale)}
                        </Text>
                      </Stack>
                      <Group gap={4} wrap="nowrap">
                        {address.isPrimary ? (
                          <Badge size="sm" variant="light" color="teal">
                            {t("primaryBadge")}
                          </Badge>
                        ) : (
                          canEdit && (
                            <Button
                              variant="subtle"
                              size="compact-xs"
                              aria-label={t("makePrimaryNamed", { address: addressName })}
                              loading={
                                makePrimaryMutation.isPending && makePrimaryMutation.variables?.id === address.id
                              }
                              onClick={() => makePrimaryMutation.mutate(address)}
                            >
                              {t("makePrimary")}
                            </Button>
                          )
                        )}
                        {canEdit && (
                          <>
                            <ActionIcon
                              variant="subtle"
                              color="gray"
                              aria-label={t("editAddressNamed", { address: addressName })}
                              onClick={() => setModalState({ mode: "edit", address })}
                            >
                              <IconPencil size={16} />
                            </ActionIcon>
                            <ActionIcon
                              variant="subtle"
                              color="red"
                              aria-label={t("deleteAddressNamed", { address: addressName })}
                              onClick={() => confirmDelete(address)}
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
                          </>
                        )}
                      </Group>
                    </Group>
                  );
                })}
              </Stack>
            </Stack>
          ))}
        </Stack>
      )}

      {showOffers && (
        <Group gap="xs">
          {offers.map((offer) => (
            <Button
              key={offer.type}
              variant="subtle"
              size="compact-xs"
              leftSection={<IconBuildingBank size={14} />}
              onClick={() =>
                setModalState({ mode: "add", initialValues: registryAddressValues(offer.address, offer.type) })
              }
            >
              {offer.label}
            </Button>
          ))}
        </Group>
      )}

      <CustomerAddressModal
        customerId={customerId}
        addresses={addresses}
        state={modalState}
        onClose={() => setModalState(null)}
      />
    </Stack>
  );
};
