import { Center, Loader, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { Spotlight } from "@mantine/spotlight";
import { IconBolt, IconBuilding, IconMail, IconPackage, IconPlus, IconSearch, IconUser } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { contactsQueryOptions } from "@vantigo/customers-ui/api/contacts";
import { customersQueryOptions } from "@vantigo/customers-ui/api/customers";
import { formatContactName } from "@vantigo/customers-ui/lib/format-contact-name";
import { meteringPointsQueryOptions } from "@vantigo/energy-ui";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../i18n";
import { spotlightNavSections } from "../apps";
import { hasPermissions, type ModuleKey, navSearchFor, visibleNavSections } from "../navigation";

const MIN_SEARCH_LENGTH = 2;
const MAX_RESULTS = 5;

interface AppSpotlightProps {
  permissions: string[] | undefined;
  isOwner: boolean;
  canManageAuthorization: boolean;
  isSystemAdmin?: boolean;
  enabledModules?: readonly ModuleKey[];
  onNavigate?: () => void;
}

/**
 * The global search (opened with mod+K or the sidebar search box): quick navigation
 * to the app sections plus live search across customers and contacts.
 */
export const AppSpotlight = ({
  permissions,
  isOwner,
  canManageAuthorization,
  isSystemAdmin,
  enabledModules,
  onNavigate,
}: AppSpotlightProps) => {
  const { t } = useI18n("host");
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [debouncedQuery] = useDebouncedValue(query, 300);

  const search = debouncedQuery.trim();
  const searchEnabled = search.length >= MIN_SEARCH_LENGTH;
  const navigationActions = visibleNavSections(spotlightNavSections, {
    permissions,
    isOwner,
    canManageAuthorization,
    isSystemAdmin,
    enabledModules,
  }).flatMap((section) =>
    section.items.map((item) => ({
      ...item,
      description: t("navigation.open", { label: t(item.label).toLowerCase() }),
    })),
  );
  const customersEnabled = enabledModules?.includes("customers") === true;
  const canSearchCustomers = customersEnabled && hasPermissions(permissions, ["customers:view"]);
  const canSearchContacts =
    customersEnabled && hasPermissions(permissions, ["customers:contacts-view", "customers:associations-view"]);
  const canSearchMeteringPoints =
    enabledModules?.includes("energy") === true && hasPermissions(permissions, ["energy:metering-points-view"]);
  const handleNavigate = (action: () => void) => {
    onNavigate?.();
    action();
  };
  // The create actions land on the list with its create form already open
  // (the list routes validate `create` in their search params).
  const quickActions = [
    ...(enabledModules?.includes("customers") && hasPermissions(permissions, ["customers:create"])
      ? [{ label: t("dashboard.createCustomer"), icon: IconPlus, path: "/customers", search: { create: true } }]
      : []),
    ...(enabledModules?.includes("communications") && hasPermissions(permissions, ["communications:conversations-view"])
      ? [{ label: t("dashboard.composeMessage"), icon: IconMail, path: "/communications/inbox", search: undefined }]
      : []),
    ...(enabledModules?.includes("products") && hasPermissions(permissions, ["products:products-manage"])
      ? [{ label: t("dashboard.addProduct"), icon: IconPackage, path: "/products", search: { create: true } }]
      : []),
  ];

  const customers = useQuery({
    ...customersQueryOptions({ search, pageSize: MAX_RESULTS }),
    enabled: searchEnabled && canSearchCustomers,
  });
  const contacts = useQuery({
    ...contactsQueryOptions({ search, pageSize: MAX_RESULTS }),
    enabled: searchEnabled && canSearchContacts,
  });
  const meteringPoints = useQuery({
    ...meteringPointsQueryOptions({ search, pageSize: MAX_RESULTS }),
    enabled: searchEnabled && canSearchMeteringPoints,
  });

  const matchingNavigation = navigationActions.filter((action) =>
    t(action.label).toLowerCase().includes(query.trim().toLowerCase()),
  );
  const customerResults = searchEnabled && canSearchCustomers ? (customers.data?.data ?? []) : [];
  const contactResults = searchEnabled && canSearchContacts ? (contacts.data?.data ?? []) : [];
  const meteringPointResults = searchEnabled && canSearchMeteringPoints ? (meteringPoints.data?.data ?? []) : [];

  const isSearching = searchEnabled && (customers.isFetching || contacts.isFetching || meteringPoints.isFetching);
  const isEmpty =
    matchingNavigation.length === 0 &&
    customerResults.length === 0 &&
    contactResults.length === 0 &&
    meteringPointResults.length === 0;

  return (
    <Spotlight.Root shortcut="mod + K" query={query} onQueryChange={setQuery} onSpotlightClose={() => setQuery("")}>
      <Spotlight.Search
        placeholder={t("navigation.search")}
        leftSection={<IconSearch size={20} stroke={1.5} />}
        rightSection={isSearching && <Loader size="xs" />}
      />
      <Spotlight.ActionsList>
        {quickActions.length > 0 && (
          <Spotlight.ActionsGroup label={t("navigation.quickActions")}>
            {quickActions.map((action) => (
              <Spotlight.Action
                key={action.label}
                label={action.label}
                leftSection={<action.icon size={20} stroke={1.5} />}
                onClick={() =>
                  handleNavigate(() => void navigate({ to: action.path as never, search: action.search as never }))
                }
              />
            ))}
          </Spotlight.ActionsGroup>
        )}
        {matchingNavigation.length > 0 && (
          <Spotlight.ActionsGroup label={t("navigation.navigation")}>
            {matchingNavigation.map((action) => (
              <Spotlight.Action
                key={action.to}
                label={t(action.label)}
                description={action.description}
                leftSection={<action.icon size={20} stroke={1.5} />}
                onClick={() =>
                  handleNavigate(() =>
                    navigate({
                      to: action.to as never,
                      search: navSearchFor(action.searchStrategy) as never,
                    }),
                  )
                }
              />
            ))}
          </Spotlight.ActionsGroup>
        )}

        {customerResults.length > 0 && (
          <Spotlight.ActionsGroup label={t("navigation.customers")}>
            {customerResults.map((customer) => (
              <Spotlight.Action
                key={customer.id}
                label={customer.name}
                description={t("navigation.customer")}
                leftSection={<IconBuilding size={20} stroke={1.5} />}
                onClick={() =>
                  handleNavigate(() =>
                    navigate({
                      to: "/customers/$customerId",
                      params: { customerId: customer.id },
                    }),
                  )
                }
              />
            ))}
          </Spotlight.ActionsGroup>
        )}

        {contactResults.length > 0 && (
          <Spotlight.ActionsGroup label={t("navigation.contacts")}>
            {contactResults.map((item) => (
              <Spotlight.Action
                key={item.contact.id}
                label={formatContactName(item.contact)}
                description={
                  [item.contact.email, item.contact.phone].filter(Boolean).join(" · ") ||
                  t("navigation.noContactDetails")
                }
                leftSection={<IconUser size={20} stroke={1.5} />}
                onClick={() =>
                  handleNavigate(() =>
                    navigate({
                      to: "/customers/contacts/$contactId",
                      params: { contactId: item.contact.id },
                    }),
                  )
                }
              />
            ))}
          </Spotlight.ActionsGroup>
        )}

        {meteringPointResults.length > 0 && (
          <Spotlight.ActionsGroup label={t("navigation.meteringPoints")}>
            {meteringPointResults.map((point) => (
              <Spotlight.Action
                key={point.id}
                label={point.gsrn}
                description={
                  [point.meterNumber, [point.address.streetAddress, point.address.city].filter(Boolean).join(", ")]
                    .filter(Boolean)
                    .join(" · ") || t("navigation.meteringPoint")
                }
                leftSection={<IconBolt size={20} stroke={1.5} />}
                onClick={() =>
                  handleNavigate(() =>
                    navigate({
                      to: "/energy/metering-points/$meteringPointId",
                      params: { meteringPointId: point.id },
                    }),
                  )
                }
              />
            ))}
          </Spotlight.ActionsGroup>
        )}

        {isEmpty &&
          (isSearching ? (
            <Center py="md">
              <Loader size="sm" />
            </Center>
          ) : (
            <Spotlight.Empty>
              <Text c="dimmed" size="sm">
                {t("navigation.nothingFound")}
              </Text>
            </Spotlight.Empty>
          ))}
      </Spotlight.ActionsList>
    </Spotlight.Root>
  );
};
