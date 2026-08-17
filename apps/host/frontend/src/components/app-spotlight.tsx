import { Center, Loader, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { Spotlight } from "@mantine/spotlight";
import { IconBuilding, IconSearch, IconUser } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { contactsQueryOptions } from "@vantigo/customers-ui/api/contacts";
import { customersQueryOptions } from "@vantigo/customers-ui/api/customers";
import { formatContactName } from "@vantigo/customers-ui/lib/format-contact-name";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../i18n";
import type { ModuleKey } from "../api/tenant-capabilities";
import { hasPermissions, navSearchFor, visibleNavSections } from "../navigation";

const MIN_SEARCH_LENGTH = 2;
const MAX_RESULTS = 5;

interface AppSpotlightProps {
  permissions: string[] | undefined;
  isOwner: boolean;
  canManageAuthorization: boolean;
  isSystemAdmin?: boolean;
  tenantSlug?: string;
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
  tenantSlug,
  enabledModules,
  onNavigate,
}: AppSpotlightProps) => {
  const { t } = useI18n("host");
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [debouncedQuery] = useDebouncedValue(query, 300);

  const search = debouncedQuery.trim();
  const searchEnabled = search.length >= MIN_SEARCH_LENGTH;
  const navigationActions = visibleNavSections({
    permissions,
    isOwner,
    canManageAuthorization,
    tenantSlug,
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
  const handleNavigate = (action: () => void) => {
    onNavigate?.();
    action();
  };

  const customers = useQuery({
    ...customersQueryOptions({ search, pageSize: MAX_RESULTS }),
    enabled: searchEnabled && canSearchCustomers,
  });
  const contacts = useQuery({
    ...contactsQueryOptions({ search, pageSize: MAX_RESULTS }),
    enabled: searchEnabled && canSearchContacts,
  });

  const matchingNavigation = navigationActions.filter((action) =>
    t(action.label).toLowerCase().includes(query.trim().toLowerCase()),
  );
  const customerResults = searchEnabled && canSearchCustomers ? (customers.data?.data ?? []) : [];
  const contactResults = searchEnabled && canSearchContacts ? (contacts.data?.data ?? []) : [];

  const isSearching = searchEnabled && (customers.isFetching || contacts.isFetching);
  const isEmpty = matchingNavigation.length === 0 && customerResults.length === 0 && contactResults.length === 0;

  return (
    <Spotlight.Root shortcut="mod + K" query={query} onQueryChange={setQuery} onSpotlightClose={() => setQuery("")}>
      <Spotlight.Search
        placeholder={t("navigation.search")}
        leftSection={<IconSearch size={20} stroke={1.5} />}
        rightSection={isSearching && <Loader size="xs" />}
      />
      <Spotlight.ActionsList>
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
                  handleNavigate(() => {
                    if (tenantSlug)
                      void navigate({
                        to: "/$tenantSlug/customers/$customerId",
                        params: { tenantSlug, customerId: customer.id },
                      });
                  })
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
                  handleNavigate(() => {
                    if (tenantSlug)
                      void navigate({
                        to: "/$tenantSlug/contacts/$contactId",
                        params: { tenantSlug, contactId: item.contact.id },
                      });
                  })
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
