import {
  Alert,
  Anchor,
  Badge,
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
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import {
  ApiConflictError,
  ApiValidationError,
  type ConflictDuplicate,
  type CustomerResponse,
  type CustomerType,
  conflictDuplicates,
  createCustomer,
  customerQueryOptions,
  customersQueryOptions,
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
 * What the modal has to show instead of the form's own validation once a
 * submit comes back as a 409: D5's revision conflict (nothing else to say —
 * the fix is to look at the latest version) or D6's duplicate legal identity
 * (who already has it, with an escape hatch to save anyway).
 */
type SaveConflict = { kind: "revision" } | { kind: "duplicate"; duplicates: ConflictDuplicate[] };

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
  // A submit that comes back as a 409 shows this instead of a field error:
  // D5's revision conflict has nothing to fix but look at the latest version,
  // and D6's duplicate identity needs the caller to see who already has it.
  const [conflict, setConflict] = useState<SaveConflict | null>(null);
  const [reloading, setReloading] = useState(false);
  // A fresh `state` (the modal opening, or opening on a different customer)
  // clears a conflict left over from the previous time it was open, adjusted
  // during render rather than an effect, the way the list page's own
  // "arrived with create open" flag is (see customers.index.tsx).
  const [seenState, setSeenState] = useState(state);
  if (state !== seenState) {
    setSeenState(state);
    if (conflict) setConflict(null);
  }
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
    mutationFn: ({
      values: { name, status, identity, type },
      override,
    }: {
      values: CustomerFormValues;
      override: boolean;
    }) =>
      isEdit
        ? updateCustomer(state.customer.id, {
            name,
            status,
            ...(identity ? { identity } : {}),
            revision: state.customer.revision,
            ...(override ? { allowDuplicateIdentity: true } : {}),
          })
        : createCustomer({
            name,
            status,
            type,
            ...(identity ? { identity } : {}),
            ...(override ? { allowDuplicateIdentity: true } : {}),
          }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      setConflict(null);
      onClose();
      notifications.show({
        color: "teal",
        title: isEdit ? t("customerUpdated") : t("customerCreated"),
        message: t("customerSaved"),
      });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      if (error instanceof ApiConflictError) {
        setConflict(
          error.code === "duplicate_legal_identity"
            ? { kind: "duplicate", duplicates: conflictDuplicates(error) }
            : { kind: "revision" },
        );
        return;
      }
      notifications.show({ color: "red", title: t("customerCouldNotBeSaved"), message: error.message });
    },
  });
  const save = (values: CustomerFormValues, override: boolean) =>
    mutation.mutate({ values: { ...values, name: values.name.trim() }, override });

  // The conflict alert's Reload action: the caller's typed values are
  // discarded (the alert says so) and the form is re-seeded from the row the
  // server holds now, so the next submit's revision is the current one.
  const reload = async () => {
    if (!isEdit) return;
    setReloading(true);
    try {
      const fresh = await queryClient.fetchQuery({ ...customerQueryOptions(state.customer.id), staleTime: 0 });
      form.setValues({ name: fresh.name, identity: undefined, status: fresh.status, type: fresh.type });
      form.resetDirty();
      form.clearErrors();
      setConflict(null);
    } finally {
      setReloading(false);
    }
  };

  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={isEdit ? t("editCustomer") : t("createNewCustomer")}
      centered
    >
      <form onSubmit={form.onSubmit((values) => save(values, false))}>
        <Stack>
          {conflict?.kind === "revision" && (
            <Alert color="yellow" title={t("customerChangedTitle")}>
              <Stack gap="xs">
                <Text size="sm">{t("customerChangedMessage")}</Text>
                <Text size="sm">{t("customerChangesNotSaved")}</Text>
                <Group justify="flex-end">
                  <Button size="xs" variant="light" color="yellow" loading={reloading} onClick={reload}>
                    {t("reload")}
                  </Button>
                </Group>
              </Stack>
            </Alert>
          )}
          {conflict?.kind === "duplicate" && (
            <Alert color="yellow" title={t("duplicateIdentityTitle")}>
              <Stack gap="xs">
                <Text size="sm">{t("duplicateIdentityMessage")}</Text>
                <Stack gap={4}>
                  {conflict.duplicates.map((duplicate) => (
                    <Group key={duplicate.id} justify="space-between" wrap="nowrap">
                      <Anchor
                        size="sm"
                        onClick={onClose}
                        renderRoot={(props) => <Link to={`/customers/${duplicate.id}` as never} {...props} />}
                      >
                        {duplicate.name}
                      </Anchor>
                      <Group gap="xs" wrap="nowrap">
                        <Text size="xs" c="dimmed">
                          #{duplicate.customerNumber}
                        </Text>
                        <Badge size="sm" variant="light" color={duplicate.status === "active" ? "teal" : "gray"}>
                          {statusBadgeLabel(duplicate.status, t)}
                        </Badge>
                      </Group>
                    </Group>
                  ))}
                </Stack>
                <Group justify="flex-end">
                  <Button
                    size="xs"
                    variant="light"
                    color="yellow"
                    loading={mutation.isPending}
                    onClick={() => save(form.values, true)}
                  >
                    {isEdit ? t("saveAnyway") : t("createAnyway")}
                  </Button>
                </Group>
              </Stack>
            </Alert>
          )}
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
          {!isEdit && <SimilarNamesHint name={form.values.name} t={t} onNavigate={onClose} />}
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

/** Matches the customers list and detail page's own status badge label. */
const statusBadgeLabel = (status: string, t: (key: string) => string) =>
  status === "active" ? t("statusActive") : status === "archived" ? t("statusArchived") : t("statusDisabled");

/**
 * A hint, not a check: while a name is typed in the create form, it asks the
 * list endpoint for that name and shows up to three existing customers whose
 * name contains it — a nudge to look before creating a possible duplicate,
 * never a block on submit (design D6). The list endpoint's search also
 * matches contacts and organisation numbers, which would be a confusing
 * reason for a name to show up here, so the client filters to name matches.
 */
const SimilarNamesHint = ({
  name,
  t,
  onNavigate,
}: {
  name: string;
  t: (key: string) => string;
  onNavigate: () => void;
}) => {
  const [debounced] = useDebouncedValue(name, 300);
  const trimmed = debounced.trim();
  const enabled = trimmed.length >= 3;
  const { data } = useQuery({
    ...customersQueryOptions({ search: trimmed, pageSize: 3, status: undefined }),
    enabled,
  });
  const matches = enabled
    ? (data?.data ?? []).filter(
        (customer) => typeof customer.name === "string" && customer.name.toLowerCase().includes(trimmed.toLowerCase()),
      )
    : [];
  if (matches.length === 0) return null;
  return (
    <Alert color="blue" variant="light" title={t("similarCustomersTitle")}>
      <Stack gap={4}>
        {matches.map((customer) => (
          <Group key={customer.id} gap="xs" wrap="nowrap">
            <Anchor
              size="sm"
              onClick={onNavigate}
              renderRoot={(props) => <Link to={`/customers/${customer.id}` as never} {...props} />}
            >
              {customer.name}
            </Anchor>
            <Text size="xs" c="dimmed">
              #{customer.customerNumber}
            </Text>
          </Group>
        ))}
      </Stack>
    </Alert>
  );
};
