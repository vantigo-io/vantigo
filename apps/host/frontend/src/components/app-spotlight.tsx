import { Center, Loader, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { Spotlight } from "@mantine/spotlight";
import { IconAddressBook, IconBolt, IconBuilding, IconSearch, IconUser, IconUsers } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { contactsQueryOptions } from "@vantigo/customers-ui/api/contacts";
import { customersQueryOptions } from "@vantigo/customers-ui/api/customers";
import { formatContactName } from "@vantigo/customers-ui/lib/format-contact-name";
import { useState } from "react";

const MIN_SEARCH_LENGTH = 2;
const MAX_RESULTS = 5;

const navigationActions = [
  { label: "Customers", description: "Browse all customers", to: "/customers", icon: IconUsers },
  { label: "Contacts", description: "Browse all contacts", to: "/contacts", icon: IconAddressBook },
  { label: "Energy", description: "Browse metering points", to: "/energy/metering-points", icon: IconBolt },
] as const;

/**
 * The global search (opened with mod+K or the sidebar search box): quick navigation
 * to the app sections plus live search across customers and contacts.
 */
export const AppSpotlight = () => {
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

  const matchingNavigation = navigationActions.filter((action) =>
    action.label.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const customerResults = searchEnabled ? (customers.data?.data ?? []) : [];
  const contactResults = searchEnabled ? (contacts.data?.data ?? []) : [];

  const isSearching = searchEnabled && (customers.isFetching || contacts.isFetching);
  const isEmpty = matchingNavigation.length === 0 && customerResults.length === 0 && contactResults.length === 0;

  return (
    <Spotlight.Root shortcut="mod + K" query={query} onQueryChange={setQuery} onSpotlightClose={() => setQuery("")}>
      <Spotlight.Search
        placeholder="Search customers, contacts..."
        leftSection={<IconSearch size={20} stroke={1.5} />}
        rightSection={isSearching && <Loader size="xs" />}
      />
      <Spotlight.ActionsList>
        {matchingNavigation.length > 0 && (
          <Spotlight.ActionsGroup label="Navigation">
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
          <Spotlight.ActionsGroup label="Customers">
            {customerResults.map((customer) => (
              <Spotlight.Action
                key={customer.id}
                label={customer.name}
                description={
                  customer.identity ? `${customer.identity.name} · ${customer.identity.id}` : "No legal identity"
                }
                leftSection={<IconBuilding size={20} stroke={1.5} />}
                onClick={() => navigate({ to: "/customers/$customerId", params: { customerId: customer.id } })}
              />
            ))}
          </Spotlight.ActionsGroup>
        )}

        {contactResults.length > 0 && (
          <Spotlight.ActionsGroup label="Contacts">
            {contactResults.map((item) => (
              <Spotlight.Action
                key={item.contact.id}
                label={formatContactName(item.contact)}
                description={
                  [item.contact.email, item.contact.phone].filter(Boolean).join(" · ") || "No contact details"
                }
                leftSection={<IconUser size={20} stroke={1.5} />}
                onClick={() => navigate({ to: "/contacts/$contactId", params: { contactId: item.contact.id } })}
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
                Nothing found...
              </Text>
            </Spotlight.Empty>
          ))}
      </Spotlight.ActionsList>
    </Spotlight.Root>
  );
};
