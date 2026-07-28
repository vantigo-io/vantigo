import {
  Button,
  Combobox,
  Divider,
  Group,
  Loader,
  Modal,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
  Tooltip,
  useCombobox,
} from "@mantine/core";
import { type UseFormReturnType, useForm } from "@mantine/form";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconRefresh } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import {
  ApiValidationError,
  type CustomerInput,
  type CustomerResponse,
  createCustomer,
  updateCustomer,
} from "../api/customers";
import { brregLookupQueryOptions, fetchBrregLookup } from "../api/lookup";

export type CustomerModalState = { mode: "create" } | { mode: "edit"; customer: CustomerResponse };

interface CustomerFormValues {
  name: string;
  hasIdentity: boolean;
  identity: {
    country: string;
    type: string;
    id: string;
    name: string;
    /** Where the identity data came from; flips to "manual" when edited by hand. */
    source: string;
  };
}

const emptyValues: CustomerFormValues = {
  name: "",
  hasIdentity: false,
  identity: { country: "no", type: "business", id: "", name: "", source: "manual" },
};

const countryOptions = [{ value: "no", label: "Norway (NO)" }];

const typeOptions = [
  { value: "business", label: "Business" },
  { value: "person", label: "Person" },
];

/** Whether a registry lookup provider exists for the given country/type. */
const hasLookupProvider = (country: string, type: string) => country === "no" && type === "business";

const valuesFromState = (state: CustomerModalState): CustomerFormValues => {
  if (state.mode === "create") {
    return emptyValues;
  }

  const { name, identity } = state.customer;
  return {
    name,
    hasIdentity: identity !== null,
    identity: identity
      ? {
          country: identity.country,
          type: identity.type,
          id: identity.id,
          name: identity.name,
          source: identity.source,
        }
      : emptyValues.identity,
  };
};

interface CustomerFormModalProps {
  state: CustomerModalState | null;
  onClose: () => void;
}

/**
 * Modal for creating a new customer or editing an existing one. The legal identity
 * is opt-in; when the country/type combination has a registry provider (currently
 * Norwegian businesses via Brønnøysundregisteret), the legal name doubles as a
 * registry search that auto-fills the legal id and name.
 */
export const CustomerFormModal = ({ state, onClose }: CustomerFormModalProps) => {
  const queryClient = useQueryClient();
  const isEdit = state?.mode === "edit";

  const form = useForm<CustomerFormValues>({
    initialValues: emptyValues,
    validate: {
      name: (value) => (value.trim().length === 0 ? "Name is required" : null),
      identity: {
        id: (value, values) => (values.hasIdentity && value.trim().length === 0 ? "Legal id is required" : null),
        name: (value, values) => (values.hasIdentity && value.trim().length === 0 ? "Legal name is required" : null),
      },
    },
  });

  // Sync form values when the modal opens for a different customer (or create).
  // The form object is recreated each render but its methods are stable, so it is
  // intentionally excluded from the dependency array.
  useEffect(() => {
    if (state) {
      form.setValues(valuesFromState(state));
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  const mutation = useMutation({
    mutationFn: (input: CustomerInput) => (isEdit ? updateCustomer(state.customer.id, input) : createCustomer(input)),
    onSuccess: () => {
      notifications.show({
        color: "teal",
        title: isEdit ? "Customer updated" : "Customer created",
        message: `"${form.values.name.trim()}" was ${isEdit ? "updated" : "created"} successfully.`,
      });
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      if (isEdit) queryClient.invalidateQueries({ queryKey: ["customers", state.customer.id, "timeline"] });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({
        color: "red",
        title: isEdit ? "Failed to update customer" : "Failed to create customer",
        message: error.message,
      });
    },
  });

  const handleSubmit = form.onSubmit((values) => {
    mutation.mutate({
      name: values.name.trim(),
      identity: values.hasIdentity
        ? {
            country: values.identity.country,
            type: values.identity.type,
            id: values.identity.id.trim(),
            name: values.identity.name.trim(),
            source: values.identity.source,
          }
        : null,
    });
  });

  return (
    <Modal opened={state !== null} onClose={onClose} title={isEdit ? "Edit customer" : "Create new customer"} centered>
      <form onSubmit={handleSubmit}>
        <Stack>
          <TextInput
            label="Name"
            description="A friendly name used to identify the customer"
            placeholder="e.g. Acme"
            withAsterisk
            data-autofocus
            {...form.getInputProps("name")}
          />

          <Divider />

          <Switch
            label="Legal identity"
            description="Connect the customer to a legal entity in a public registry"
            {...form.getInputProps("hasIdentity", { type: "checkbox" })}
          />

          {form.values.hasIdentity && <LegalIdentityFields form={form} />}

          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? "Save changes" : "Create customer"}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};

const LegalIdentityFields = ({ form }: { form: UseFormReturnType<CustomerFormValues> }) => {
  const { country, type, id } = form.values.identity;
  const lookupAvailable = hasLookupProvider(country, type);

  const refresh = useMutation({
    mutationFn: () => fetchBrregLookup({ legalId: id.trim() }),
    onSuccess: (response) => {
      const match = response.data[0];
      if (match) {
        form.setFieldValue("identity.name", match.legalName);
        form.setFieldValue("identity.id", match.legalId);
        form.setFieldValue("identity.source", "brreg");
        notifications.show({
          color: "teal",
          title: "Registry data refreshed",
          message: `Legal data was refreshed from the registry for "${match.legalName}".`,
        });
      } else {
        notifications.show({
          color: "yellow",
          title: "No registry match",
          message: `No entity with legal id "${id.trim()}" was found in the registry.`,
        });
      }
    },
    onError: (error) => {
      notifications.show({
        color: "red",
        title: "Failed to refresh registry data",
        message: error.message,
      });
    },
  });

  const nameInputProps = form.getInputProps("identity.name");
  const idInputProps = form.getInputProps("identity.id");

  return (
    <Stack gap="sm">
      <Group grow>
        <Select
          label="Country"
          description="Where the entity is registered"
          data={countryOptions}
          allowDeselect={false}
          {...form.getInputProps("identity.country")}
        />
        <Select
          label="Type"
          description="The kind of legal entity"
          data={typeOptions}
          allowDeselect={false}
          {...form.getInputProps("identity.type")}
        />
      </Group>

      {lookupAvailable ? (
        <LegalNameLookupInput form={form} />
      ) : (
        <TextInput
          label="Legal name"
          placeholder="The registered name of the entity"
          withAsterisk
          {...nameInputProps}
          onChange={(event) => {
            nameInputProps.onChange(event);
            form.setFieldValue("identity.source", "manual");
          }}
        />
      )}

      <TextInput
        label="Legal id"
        description="The registration number of the entity"
        placeholder="e.g. 923609016"
        withAsterisk
        rightSection={
          lookupAvailable && (
            <Tooltip label="Refresh legal data from the registry">
              <Button
                variant="subtle"
                size="compact-xs"
                px={4}
                aria-label="Refresh data"
                loading={refresh.isPending}
                disabled={id.trim().length === 0}
                onClick={() => refresh.mutate()}
              >
                <IconRefresh size={16} />
              </Button>
            </Tooltip>
          )
        }
        {...idInputProps}
        onChange={(event) => {
          idInputProps.onChange(event);
          form.setFieldValue("identity.source", "manual");
        }}
      />
    </Stack>
  );
};

/**
 * The legal name input doubling as a registry search: typing queries
 * Brønnøysundregisteret and picking a suggestion fills both the legal name
 * and the legal id. The value remains freely editable afterwards.
 */
const LegalNameLookupInput = ({ form }: { form: UseFormReturnType<CustomerFormValues> }) => {
  const combobox = useCombobox();
  const name = form.values.identity.name;
  const [debouncedName] = useDebouncedValue(name, 300);

  const lookup = useQuery(brregLookupQueryOptions(debouncedName));
  const suggestions = lookup.data?.data ?? [];

  const inputProps = form.getInputProps("identity.name");

  return (
    <Combobox
      store={combobox}
      onOptionSubmit={(legalId) => {
        const suggestion = suggestions.find((s) => s.legalId === legalId);
        if (suggestion) {
          form.setFieldValue("identity.name", suggestion.legalName);
          form.setFieldValue("identity.id", suggestion.legalId);
          form.setFieldValue("identity.source", "brreg");
        }
        combobox.closeDropdown();
      }}
    >
      <Combobox.Target>
        <TextInput
          label="Legal name"
          description="Search the registry by name and pick a match, or type freely"
          placeholder="e.g. Equinor ASA"
          withAsterisk
          rightSection={lookup.isFetching && <Loader size="xs" />}
          {...inputProps}
          onChange={(event) => {
            inputProps.onChange(event);
            form.setFieldValue("identity.source", "manual");
            combobox.openDropdown();
          }}
          onFocus={() => combobox.openDropdown()}
          onBlur={(event) => {
            inputProps.onBlur?.(event);
            combobox.closeDropdown();
          }}
        />
      </Combobox.Target>

      <Combobox.Dropdown hidden={suggestions.length === 0}>
        <Combobox.Options>
          {suggestions.map((suggestion) => (
            <Combobox.Option key={suggestion.legalId} value={suggestion.legalId}>
              <Text size="sm">{suggestion.legalName}</Text>
              <Text size="xs" c="dimmed">
                {suggestion.legalId}
              </Text>
            </Combobox.Option>
          ))}
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  );
};
