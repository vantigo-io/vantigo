import {
  Button,
  Combobox,
  Group,
  Loader,
  Modal,
  SegmentedControl,
  Select,
  Stack,
  Text,
  TextInput,
  useCombobox,
} from "@mantine/core";
import { type UseFormReturnType, useForm } from "@mantine/form";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import {
  ApiValidationError,
  type CustomerResponse,
  type CustomerType,
  createCustomer,
  updateCustomer,
} from "../api/customers";
import { brregLookupQueryOptions, type LookupResult } from "../api/lookup";
import "../i18n";

export type CustomerModalState = { mode: "create" } | { mode: "edit"; customer: CustomerResponse };

type CustomerIdentity = { country: string; type: string; id: string; name: string; source: string };
type CustomerFormValues = {
  name: string;
  identity: CustomerIdentity | undefined;
  status: string;
  type: CustomerType;
};

/**
 * Creates or edits a customer. The type — business or private person — is
 * chosen on create, above the name, and decides whether the name is looked up
 * in Brønnøysundregistrene at all. It is not editable here: changing it later
 * is a separate, confirmed action on the customer page.
 */
export const CustomerFormModal = ({ state, onClose }: { state: CustomerModalState | null; onClose: () => void }) => {
  const queryClient = useQueryClient();
  const { t } = useI18n("customers");
  const isEdit = state?.mode === "edit";
  const form = useForm<CustomerFormValues>({
    initialValues: { name: "", identity: undefined, status: "active", type: "business" },
    validate: { name: (value: string) => (value.trim() ? null : t("customerNameRequired")) },
  });
  useEffect(() => {
    if (state) {
      form.setValues({
        name: state.mode === "edit" ? state.customer.name : "",
        identity: undefined,
        status: state.mode === "edit" ? state.customer.status : "active",
        type: state.mode === "edit" ? state.customer.type : "business",
      });
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);
  const mutation = useMutation({
    mutationFn: ({ name, status, identity, type }: CustomerFormValues) =>
      isEdit
        ? updateCustomer(state.customer.id, { name, status, ...(identity ? { identity } : {}) })
        : createCustomer({ name, status, type, ...(identity ? { identity } : {}) }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onClose();
      notifications.show({
        color: "teal",
        title: isEdit ? t("customerUpdated") : t("customerCreated"),
        message: t("customerSaved"),
      });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      else notifications.show({ color: "red", title: t("customerCouldNotBeSaved"), message: error.message });
    },
  });
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={isEdit ? t("editCustomer") : t("createNewCustomer")}
      centered
    >
      <form onSubmit={form.onSubmit((values) => mutation.mutate({ ...values, name: values.name.trim() }))}>
        <Stack>
          {!isEdit && (
            <SegmentedControl
              aria-label={t("customerTypeLabel")}
              fullWidth
              data={[
                { value: "business", label: t("customerTypeBusiness") },
                { value: "person", label: t("customerTypePerson") },
              ]}
              value={form.values.type}
              onChange={(value) => {
                // A Brreg hit is a business identity; it cannot follow the
                // customer into the private type.
                form.setValues({ type: value as CustomerType, identity: undefined });
              }}
            />
          )}
          {form.values.type === "business" ? (
            <CompanyLookupInput form={form} t={t} />
          ) : (
            <TextInput
              label={t("name")}
              description={t("customerPersonNameDescription")}
              placeholder={t("customerPersonNamePlaceholder")}
              withAsterisk
              data-autofocus
              {...form.getInputProps("name")}
            />
          )}
          <Select
            label={t("status")}
            data={[
              { value: "active", label: t("statusActive") },
              { value: "disabled", label: t("statusDisabled") },
              { value: "archived", label: t("statusArchived") },
            ]}
            allowDeselect={false}
            {...form.getInputProps("status")}
          />
          {form.values.type === "business" && (
            <Text size="sm" c="dimmed">
              {t("legalIdentityPermission")}
            </Text>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? t("saveChanges") : t("createCustomer")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};

type CustomerForm = UseFormReturnType<CustomerFormValues>;

const CompanyLookupInput = ({ form, t }: { form: CustomerForm; t: (key: string) => string }) => {
  const combobox = useCombobox();
  const [search] = useDebouncedValue(form.values.name, 300);
  const lookup = useQuery(brregLookupQueryOptions(search));
  const results = lookup.data?.data ?? [];
  const inputProps = form.getInputProps("name");

  const selectResult = (result: LookupResult) => {
    form.setValues({
      name: result.legalName,
      identity: { country: "no", type: "business", id: result.legalId, name: result.legalName, source: "brreg" },
    });
    combobox.closeDropdown();
  };

  return (
    <Combobox
      store={combobox}
      onOptionSubmit={(id) => {
        const result = results.find((item) => item.legalId === id);
        if (result) selectResult(result);
      }}
    >
      <Combobox.Target>
        <TextInput
          label={t("name")}
          description={t("customerLookupDescription")}
          placeholder={t("customerNamePlaceholder")}
          withAsterisk
          data-autofocus
          rightSection={lookup.isFetching ? <Loader size="xs" /> : undefined}
          {...inputProps}
          onChange={(event) => {
            inputProps.onChange(event);
            if (form.values.identity && event.currentTarget.value !== form.values.identity.name) {
              form.setFieldValue("identity", undefined);
            }
            combobox.openDropdown();
          }}
          onFocus={() => combobox.openDropdown()}
          onBlur={(event) => {
            inputProps.onBlur?.(event);
            combobox.closeDropdown();
          }}
        />
      </Combobox.Target>
      <Combobox.Dropdown hidden={results.length === 0}>
        <Combobox.Options>
          {results.map((result) => (
            <Combobox.Option key={result.legalId} value={result.legalId}>
              <Text size="sm">{result.legalName}</Text>
              <Text size="xs" c="dimmed">
                {result.legalId}
              </Text>
            </Combobox.Option>
          ))}
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  );
};
