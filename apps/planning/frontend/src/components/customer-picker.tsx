import { Badge, CloseButton, Group, Input, Select, Stack, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { customerSearchQueryOptions } from "../api/customers";
import type { PlanningCustomerRef } from "../api/terms";
import { usePlanningFormat } from "../lib/format";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

export interface CustomerPickerProps {
  label: string;
  description?: string;
  /** The customers chosen, named as the server answered them (an unresolved one by id, rule 4). */
  value: readonly PlanningCustomerRef[];
  onChange: (customers: PlanningCustomerRef[]) => void;
  /** The most it takes; the search is offered only below it. */
  max: number;
  error?: string;
}

/**
 * The customers a non-compete names (design S14): the chosen ones as chips,
 * each removable, and a search over the customer directory
 * (`GET /api/v1/customers`) that adds one. The search needs
 * `customers:view`, which a planner need not hold: when the directory
 * refuses it, the picker keeps the customers the server named and lets the
 * planner remove them, and says why nothing can be added.
 */
export const CustomerPicker = ({ label, description, value, onChange, max, error }: CustomerPickerProps) => {
  const { t } = usePlanningFormat();
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search.trim(), SEARCH_DEBOUNCE_MS);
  // The directory is searched once the planner opens the picker or types, not when the sheet opens.
  const [opened, setOpened] = useState(false);
  const found = useQuery(customerSearchQueryOptions(debounced, opened || debounced !== ""));
  const chosen = new Set(value.map((customer) => customer.id));
  const options = (found.data ?? [])
    .filter((customer) => !chosen.has(customer.id))
    .map((customer) => ({ value: String(customer.id), label: customer.name }));
  const nameOf = (customer: PlanningCustomerRef) => customer.name ?? t("customerById", { id: customer.id });

  return (
    <Input.Wrapper label={label} description={description} error={error}>
      <Stack gap={6} mt={4}>
        {value.length > 0 && (
          <Group gap={6} component="ul" m={0} p={0} style={{ listStyle: "none" }} aria-label={label}>
            {value.map((customer) => (
              <Badge
                component="li"
                key={customer.id}
                variant="light"
                size="lg"
                tt="none"
                rightSection={
                  <CloseButton
                    size="xs"
                    aria-label={t("removeCustomer", { name: nameOf(customer) })}
                    onClick={() => onChange(value.filter((one) => one.id !== customer.id))}
                  />
                }
              >
                {nameOf(customer)}
              </Badge>
            ))}
          </Group>
        )}
        {found.isError ? (
          <Text size="xs" c="dimmed">
            {t("customersUnsearchable")}
          </Text>
        ) : (
          value.length < max && (
            <Select
              aria-label={t("addCustomer")}
              placeholder={t("searchCustomers")}
              searchable
              value={null}
              searchValue={search}
              onSearchChange={setSearch}
              onDropdownOpen={() => setOpened(true)}
              data={options}
              // The directory has already filtered; filtering again would hide
              // matches whose name does not contain the term literally.
              filter={({ options: all }) => all}
              nothingFoundMessage={found.isFetching ? undefined : t("noCustomersFound")}
              onChange={(next) => {
                const customer = found.data?.find((one) => String(one.id) === next);
                if (!customer) return;
                onChange([...value, { id: customer.id, name: customer.name }]);
                setSearch("");
              }}
            />
          )
        )}
      </Stack>
    </Input.Wrapper>
  );
};
