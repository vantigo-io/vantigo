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
  syncCustomerRevision,
  updateCustomer,
} from "../api/customers";
import { brregLookupQueryOptions, type LookupResult } from "../api/lookup";
import { useCustomerReload } from "../lib/customer-reload";
import { customerWriteErrorMessage, isCustomerMerged } from "../lib/customer-write-error";
import "../i18n";

export type CustomerModalState = { mode: "create" } | { mode: "edit"; customer: CustomerResponse };

type CustomerIdentity = { country: string; type: string; id: string; name: string; source: string };
type CustomerFormValues = {
  name: string;
  identity: CustomerIdentity | undefined;
  status: string;
  type: CustomerType;
  /** Design D2/D6: create-only — email and phone are edited afterwards through the contact-info card. */
  email: string;
  phone: string;
};

/**
 * The optional `contactInfo` create sends (design D2) — only when at least
 * one of email/phone is filled, and website is not offered on this form (the
 * contact-info card's own edit modal is where that is set).
 */
const contactInfoToSend = (email: string, phone: string) => {
  const trimmedEmail = email.trim();
  const trimmedPhone = phone.trim();
  if (!trimmedEmail && !trimmedPhone) return undefined;
  return { ...(trimmedEmail ? { email: trimmedEmail } : {}), ...(trimmedPhone ? { phone: trimmedPhone } : {}) };
};

/**
 * Validation errors for `contactInfo.email`/`contactInfo.phone` (design D2)
 * are keyed the way `identity.<field>` already is — this maps them onto the
 * form's own bare `email`/`phone` fields.
 */
const mapContactInfoErrors = (fieldErrors: Record<string, string>): Record<string, string> =>
  Object.fromEntries(
    Object.entries(fieldErrors).map(([field, message]) => [field.replace(/^contactInfo\./, ""), message]),
  );

/**
 * What the modal has to show instead of the form's own validation once a
 * submit comes back as a 409: D5's revision conflict (nothing else to say —
 * the fix is to look at the latest version) or D6's duplicate legal identity
 * (who already has it, with an escape hatch to save anyway).
 */
type SaveConflict = { kind: "revision" } | { kind: "duplicate"; duplicates: ConflictDuplicate[] };

/** The revision a modal state opens with: an edited customer's, and nothing on create. */
const revisionOf = (state: CustomerModalState | null) => (state?.mode === "edit" ? state.customer.revision : undefined);

/**
 * Creates or edits a customer. The type — business or private person — is
 * chosen on create, above the name, and decides whether the name is looked up
 * in Brønnøysundregistrene at all. It is not editable here: changing it later
 * is a separate, confirmed action on the customer page.
 */
export const CustomerFormModal = ({
  state,
  onClose,
  canMerge,
}: {
  state: CustomerModalState | null;
  onClose: () => void;
  /** `customers:merge`: a duplicate-identity conflict says a merge may be the fix (merge design D4). */
  canMerge?: boolean;
}) => {
  const queryClient = useQueryClient();
  const { t } = useI18n("customers");
  const isEdit = state?.mode === "edit";
  // A submit that comes back as a 409 shows this instead of a field error:
  // D5's revision conflict has nothing to fix but look at the latest version,
  // and D6's duplicate identity needs the caller to see who already has it.
  const [conflict, setConflict] = useState<SaveConflict | null>(null);
  // The revision the next save sends. It cannot be read off `state` at submit
  // time: the modal state is a snapshot the opening page handed over (the list
  // row, or the detail page's customer as it was rendered), and neither is
  // re-derived while the modal is open — so after a 409 and a Reload, `state`
  // still carries the revision the server has already refused. Holding it here
  // is what lets Reload actually unblock the next save (design D5).
  const [revision, setRevision] = useState(revisionOf(state));
  // A fresh `state` (the modal opening, or opening on a different customer)
  // re-seeds that revision and clears a conflict left over from the previous
  // time it was open, adjusted during render rather than an effect, the way the
  // list page's own "arrived with create open" flag is (see customers.index.tsx).
  const [seenState, setSeenState] = useState(state);
  const form = useForm<CustomerFormValues>({
    initialValues: { name: "", identity: undefined, status: "active", type: "business", email: "", phone: "" },
    validate: { name: (value: string) => (value.trim() ? null : t("customerNameRequired")) },
  });
  useEffect(() => {
    if (state) {
      form.setValues({
        name: state.mode === "edit" ? state.customer.name : "",
        identity: undefined,
        status: state.mode === "edit" ? state.customer.status : "active",
        type: state.mode === "edit" ? state.customer.type : "business",
        email: "",
        phone: "",
      });
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);
  const mutation = useMutation({
    mutationFn: ({
      values: { name, status, identity, type, email, phone },
      override,
    }: {
      values: CustomerFormValues;
      override: boolean;
    }) => {
      if (isEdit) {
        return updateCustomer(state.customer.id, {
          name,
          status,
          ...(identity ? { identity } : {}),
          revision,
          ...(override ? { allowDuplicateIdentity: true } : {}),
        });
      }
      const contactInfo = contactInfoToSend(email, phone);
      return createCustomer({
        name,
        status,
        type,
        ...(identity ? { identity } : {}),
        ...(contactInfo ? { contactInfo } : {}),
        ...(override ? { allowDuplicateIdentity: true } : {}),
      });
    },
    onSuccess: (saved) => {
      // An edit answers the whole customer, so its fresh revision is in hand
      // before the invalidation's refetch lands — every other editor of this
      // row reads it from the cache (see `syncCustomerRevision`). A create
      // answers an id alone and has no revision to carry.
      if (isEdit) syncCustomerRevision(queryClient, state.customer.id, (saved as CustomerResponse).revision);
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
        form.setErrors(mapContactInfoErrors(error.fieldErrors));
        return;
      }
      // A merged-away customer's refusal is a 409 too, but no reload makes the
      // save go through: it is said, as every customer write says it.
      if (error instanceof ApiConflictError && !isCustomerMerged(error)) {
        setConflict(
          error.code === "duplicate_legal_identity"
            ? { kind: "duplicate", duplicates: conflictDuplicates(error) }
            : { kind: "revision" },
        );
        return;
      }
      notifications.show({
        color: "red",
        title: t("customerCouldNotBeSaved"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });
  const save = (values: CustomerFormValues, override: boolean) =>
    mutation.mutate({ values: { ...values, name: values.name.trim() }, override });

  // The conflict alert's Reload action: the caller's typed values are
  // discarded (the alert says so) and the form is re-seeded from the row the
  // server holds now — its revision included, which is the half that makes the
  // next submit go through instead of hitting the same 409 again. Shared with
  // the two card modals that edit the same row (see `useCustomerReload`);
  // the id is only ever used from the revision-conflict alert, which a create
  // cannot raise — its own 409 is the duplicate identity below.
  const reloadId = state?.mode === "edit" ? state.customer.id : 0;
  const reload = useCustomerReload({
    customerId: reloadId,
    queryKey: customerQueryOptions(reloadId).queryKey,
    fetchFresh: () => queryClient.fetchQuery({ ...customerQueryOptions(reloadId), staleTime: 0 }),
    revisionOf: (fresh) => fresh.revision,
    seed: (fresh) => {
      form.setValues({ name: fresh.name, identity: undefined, status: fresh.status, type: fresh.type });
      form.resetDirty();
      form.clearErrors();
      setRevision(fresh.revision);
      setConflict(null);
    },
  });
  if (state !== seenState) {
    setSeenState(state);
    setRevision(revisionOf(state));
    if (conflict) setConflict(null);
    reload.forget();
  }

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
                {reload.failed && (
                  <Text size="sm" c="red">
                    {t("couldNotReload")}
                  </Text>
                )}
                <Group justify="flex-end">
                  <Button size="xs" variant="light" color="yellow" loading={reload.reloading} onClick={reload.reload}>
                    {t("reload")}
                  </Button>
                </Group>
              </Stack>
            </Alert>
          )}
          {conflict?.kind === "duplicate" && (
            <Alert color="yellow" title={t("duplicateIdentityTitle")}>
              <Stack gap="xs">
                {/* The server withholds `duplicates` from a caller without
                    customers:view (design D6), so the list is not guaranteed:
                    the message then has to stand on its own, and the override
                    below is the only thing left to act on. */}
                <Text size="sm">
                  {conflict.duplicates.length > 0 ? t("duplicateIdentityMessage") : t("duplicateIdentityMessageAlone")}
                </Text>
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
                {canMerge && conflict.duplicates.length > 0 && (
                  // Both forms (merge design D4), in two wordings: the
                  // duplicates above are already links to where Merge… lives,
                  // and a customer being created does not exist yet, so on a
                  // create the line says to open the duplicate instead — or
                  // create anyway and merge there.
                  <Text size="xs" c="dimmed">
                    {t(isEdit ? "duplicateIdentityMergeHint" : "duplicateIdentityMergeHintCreate")}
                  </Text>
                )}
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
          {!isEdit && (
            <Group grow>
              <TextInput label={t("email")} placeholder={t("emailPlaceholder")} {...form.getInputProps("email")} />
              <TextInput label={t("phone")} placeholder={t("phonePlaceholder")} {...form.getInputProps("phone")} />
            </Group>
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
