import { Center, Loader, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { Spotlight } from "@mantine/spotlight";
import { IconAddressBook, IconBuilding, IconSearch, IconUser, IconUsers } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";

import { contactsQueryOptions } from "../api/contacts";
import { customersQueryOptions } from "../api/customers";
import { formatContactName } from "../lib/format-contact-name";
import "../i18n";

const MIN_SEARCH_LENGTH = 2;
const MAX_RESULTS = 5;

const navigationActions = [
  { label: "Customers", description: "Browse all customers", to: "/customers", icon: IconUsers },
  { label: "Contacts", description: "Browse all contacts", to: "/customers/contacts", icon: IconAddressBook },
] as const;

/**
 * The global search (opened with mod+K or the sidebar search box): quick navigation
 * to the app sections plus live search across customers and contacts.
 */
export const AppSpotlight = () => {
  const { t } = useI18n("customers");
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [debouncedQuery] = useDebouncedValue(query, 300);

  const search = debouncedQuery.trim();
  const searchEnabled = search.length >= MIN_SEARCH_LENGTH;

  const customers = useQuery({
    ...customersQueryOptions({ search, pageSize: MAX_RESULTS }),
    enabled: searchEnabled,
  });
  const contacts = useQuery({
    ...contactsQueryOptions({ search, pageSize: MAX_RESULTS }),
    enabled: searchEnabled,
  });

  const navigation = [
    { ...navigationActions[0], label: t("customers"), description: t("browseAllCustomers") },
    { ...navigationActions[1], label: t("contacts"), description: t("browseAllContacts") },
  ];
  const matchingNavigation = navigation.filter((action) =>
    action.label.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const customerResults = searchEnabled ? (customers.data?.data ?? []) : [];
  const contactResults = searchEnabled ? (contacts.data?.data ?? []) : [];

  const isSearching = searchEnabled && (customers.isFetching || contacts.isFetching);
  const isEmpty = matchingNavigation.length === 0 && customerResults.length === 0 && contactResults.length === 0;

  return (
    <Spotlight.Root shortcut="mod + K" query={query} onQueryChange={setQuery} onSpotlightClose={() => setQuery("")}>
      <Spotlight.Search
        placeholder={t("searchCustomersContacts")}
        leftSection={<IconSearch size={20} stroke={1.5} />}
        rightSection={isSearching && <Loader size="xs" />}
      />
      <Spotlight.ActionsList>
        {matchingNavigation.length > 0 && (
          <Spotlight.ActionsGroup label={t("navigation")}>
            {matchingNavigation.map((action) => (
              <Spotlight.Action
                key={action.to}
                label={action.label}
                description={action.description}
                leftSection={<action.icon size={20} stroke={1.5} />}
                onClick={() => navigate({ to: action.to, search: { page: 1, search: "" } })}
              />
            ))}
          </Spotlight.ActionsGroup>
        )}

        {customerResults.length > 0 && (
          <Spotlight.ActionsGroup label={t("customers")}>
            {customerResults.map((customer) => (
              <Spotlight.Action
                key={customer.id}
                label={customer.name}
                description={t("customer")}
                leftSection={<IconBuilding size={20} stroke={1.5} />}
                onClick={() => navigate({ to: "/customers/$customerId", params: { customerId: customer.id } })}
              />
            ))}
          </Spotlight.ActionsGroup>
        )}

        {contactResults.length > 0 && (
          <Spotlight.ActionsGroup label={t("contacts")}>
            {contactResults.map((item) => (
              <Spotlight.Action
                key={item.contact.id}
                label={formatContactName(item.contact)}
                description={
                  [item.contact.email, item.contact.phone].filter(Boolean).join(" · ") || t("noContactDetails")
                }
                leftSection={<IconUser size={20} stroke={1.5} />}
                onClick={() =>
                  navigate({ to: "/customers/contacts/$contactId", params: { contactId: item.contact.id } })
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
                {t("nothingFound")}
              </Text>
            </Spotlight.Empty>
          ))}
      </Spotlight.ActionsList>
    </Spotlight.Root>
  );
};
