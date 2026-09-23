import { ActionIcon, Alert, Button, Group, Modal, Stack, Table, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useId, useState } from "react";
import {
  type CustomerGroupSummary,
  createGroup,
  customerGroupsQueryOptions,
  deleteGroup,
  updateGroup,
} from "../api/groups";
import { ApiConflictError, ApiValidationError } from "../api/request";
import "../i18n";

/**
 * The group vocabulary's own editor (customer groups design D5): create,
 * rename, re-default and delete, reached from the list page's Group filter and
 * only for a caller the host says may edit (`customers:update`).
 *
 * Two differences from the Manage tags modal it is shaped after, and both are
 * the design's:
 *
 *  - It CREATES. A tag is created from the picker mid-edit, where somebody is
 *    already typing a name; a group carries a default payment term, which is a
 *    decision rather than a word, so it is made here, where the field is.
 *  - A group with members cannot be deleted (design D2), and this list already
 *    knows how many there are: the control is DISABLED with the reason beside it
 *    rather than offering a click the server answers 409 `group_in_use`. The 409
 *    is still handled — somebody can fill a group between this list and the
 *    click — but nobody is invited into it.
 *
 * Every mutation invalidates the broad `["customers"]` prefix rather than only
 * `["customers", "groups"]`: a rename changes what the Group filter, the
 * relationship card and the billing card's inherited-term sentence say, and
 * those live under other keys.
 */
export const ManageGroupsModal = ({
  opened,
  onClose,
  onGroupDeleted,
}: {
  opened: boolean;
  onClose: () => void;
  /** The id of a group that no longer exists, for a caller holding it as a filter. */
  onGroupDeleted?: (groupId: string) => void;
}) => {
  const { t } = useI18n("customers");
  const { data: groups, isPending, isError, refetch } = useQuery({ ...customerGroupsQueryOptions(), enabled: opened });
  const [editing, setEditing] = useState<CustomerGroupSummary | null>(null);
  const [deleting, setDeleting] = useState<CustomerGroupSummary | null>(null);
  // This component stays mounted while the modal is closed, so an edit or a
  // delete left half-done would greet the next opening — seeded from a snapshot
  // of the list that may be long stale by then. Closing lets go of both.
  const close = () => {
    setEditing(null);
    setDeleting(null);
    onClose();
  };
  // Prefixes the ids that tie a disabled delete to the sentence saying why, so
  // two mounted modals never share one.
  const reasonIdPrefix = useId();
  const blockedReasonId = (group: CustomerGroupSummary) => `${reasonIdPrefix}-blocked-${group.id}`;

  return (
    <Modal opened={opened} onClose={close} title={t("manageGroups")} centered>
      <Stack>
        {isPending ? (
          <ContentSkeleton rows={2} rowHeight={32} />
        ) : isError ? (
          // A failed load is not an empty vocabulary: "No groups yet" would
          // invite creating a name that already exists. The address list's shape.
          <Stack align="center" py="md">
            <Text c="red">{t("failedLoadGroups")}</Text>
            <Button variant="light" onClick={() => refetch()}>
              {t("tryAgain")}
            </Button>
          </Stack>
        ) : (
          groups.length === 0 && <EmptyState size="sm" title={t("noGroupsYet")} />
        )}
        {(groups ?? []).length > 0 && (
          <Table>
            <Table.Tbody>
              {(groups ?? []).map((group) => (
                <Table.Tr key={group.id}>
                  <Table.Td>
                    <Text size="sm">{group.name}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {group.defaultPaymentTermsDays === null
                        ? t("groupNoDefault")
                        : t("paymentTermsDaysValue", { count: group.defaultPaymentTermsDays })}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {group.customerCount === 0
                        ? t("groupOnNoCustomers")
                        : t("groupOnCustomers", { count: group.customerCount })}
                    </Text>
                  </Table.Td>
                  <Table.Td w={80}>
                    <Group gap={4} justify="flex-end" wrap="nowrap">
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        aria-label={t("editNamedGroup", { name: group.name })}
                        onClick={() => setEditing(group)}
                      >
                        <IconPencil size={16} />
                      </ActionIcon>
                      {/* Disabled, not hidden: the reason belongs beside a control
                          somebody can see, and the sentence under the table says
                          what it is — tied to the control by aria-describedby, so
                          a screen reader hears the why and not only "dimmed". */}
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("deleteNamedGroup", { name: group.name })}
                        aria-describedby={group.customerCount > 0 ? blockedReasonId(group) : undefined}
                        disabled={group.customerCount > 0}
                        onClick={() => setDeleting(group)}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        {/* The reasons sit under the table rather than in the count cell, so
            "2 customers" and "2 customers belong to Retail…" are two separate
            text nodes: getByText is exact-match, but a nested match would still
            be the kind of thing that breaks when somebody reformats a cell. */}
        {(groups ?? [])
          .filter((group) => group.customerCount > 0)
          .map((group) => (
            <Text key={group.id} id={blockedReasonId(group)} size="xs" c="dimmed">
              {t("deleteGroupBlocked", { name: group.name, count: group.customerCount })}
            </Text>
          ))}
        {/* Keyed on the group being edited, so picking another row remounts the
            form on that group's values instead of writing them over the open
            form's — and so leaving edit mode gives a blank CREATE form back.
            The keys are prefixed because the form and the delete confirmation
            are siblings and can name the SAME group: two children keyed "g2"
            leave React unable to tell them apart, and the edit form survived
            beside the create form it was replaced by. */}
        <GroupForm key={editing ? `edit-${editing.id}` : "new"} group={editing} onDone={() => setEditing(null)} />
        {deleting && (
          <DeleteGroupConfirmation
            key={`delete-${deleting.id}`}
            group={deleting}
            onDeleted={(deleted) => {
              // An edit form open on the group just deleted would save into a
              // 404: it goes back to the blank create form instead.
              setEditing((current) => (current?.id === deleted ? null : current));
              onGroupDeleted?.(deleted);
            }}
            onDone={() => setDeleting(null)}
          />
        )}
      </Stack>
    </Modal>
  );
};

interface GroupFormValues {
  name: string;
  /** A string, not a number: "" is unambiguously "no default", which `null` on the wire is. */
  days: string;
}

/**
 * One form for both create and edit — the caller keys it on the group being
 * edited, so there is never a second instance on screen and never two inputs
 * with the same accessible name. `group === null` is the create form, which is
 * the modal's resting state.
 *
 * `days` is a plain `TextInput` rather than Mantine's `NumberInput`: an emptied
 * `NumberInput` yields `""` or `NaN` depending on version, and the one thing
 * this field has to express precisely is "no default at all". It is validated
 * here before any request — a whole number 0-365, the server's own range — so a
 * typo is a message under the field rather than a round trip.
 */
const GroupForm = ({ group, onDone }: { group: CustomerGroupSummary | null; onDone: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const form = useForm<GroupFormValues>({
    initialValues: { name: group?.name ?? "", days: group?.defaultPaymentTermsDays?.toString() ?? "" },
  });

  const mutation = useMutation({
    mutationFn: (values: GroupFormValues) => {
      const input = {
        name: values.name.trim(),
        // Both fields, always: PUT is a full replace, so an emptied default has
        // to arrive as null rather than be left out and kept (design D2).
        defaultPaymentTermsDays: values.days.trim() === "" ? null : Number(values.days),
      };
      return group ? updateGroup(group.id, input) : createGroup(input);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      form.reset();
      onDone();
      notifications.show({ color: "teal", title: t("groupSaved"), message: t("groupSavedMessage") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        // The server keys the term error `defaultPaymentTermsDays`; this form's
        // field is `days`, so the one key that can arrive is mapped onto it and
        // the rest are set as they came.
        const { defaultPaymentTermsDays, ...rest } = error.fieldErrors;
        form.setErrors({ ...rest, ...(defaultPaymentTermsDays ? { days: defaultPaymentTermsDays } : {}) });
        return;
      }
      // Names are unique without regard to case, so this is what creating or
      // renaming to a case variant answers. It is about the one field the person
      // just typed in, so it goes under that input rather than into a
      // notification that would leave the form looking saved.
      if (error instanceof ApiConflictError && error.code === "group_exists") {
        form.setErrors({ name: t("groupNameTaken") });
        return;
      }
      // Anything else (a 404 for a group deleted in another tab, above all)
      // means the list this form was opened from is out of date: it is read
      // again, so the row stops offering what the server just refused.
      queryClient.invalidateQueries({ queryKey: customerGroupsQueryOptions().queryKey });
      notifications.show({ color: "red", title: t("groupCouldNotBeSaved"), message: error.message });
    },
  });

  return (
    <form
      onSubmit={form.onSubmit((values) => {
        // One condition, one message: at most three digits rules out signs,
        // decimals and words, and the range caps what three digits allow.
        const days = values.days.trim();
        if (days !== "" && (!/^\d{1,3}$/.test(days) || Number(days) > 365)) {
          form.setErrors({ days: t("groupDefaultPaymentTermsInvalid") });
          return;
        }
        mutation.mutate(values);
      })}
    >
      <Stack gap="xs">
        <TextInput label={t("groupName")} data-autofocus {...form.getInputProps("name")} />
        <TextInput
          label={t("groupDefaultPaymentTerms")}
          placeholder={t("groupNoDefault")}
          {...form.getInputProps("days")}
        />
        <Group justify="flex-end">
          {group && (
            <Button variant="default" onClick={onDone}>
              {t("cancel")}
            </Button>
          )}
          <Button type="submit" loading={mutation.isPending}>
            {group ? t("saveChanges") : t("createGroup")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * The delete confirmation, which only ever opens for an EMPTY group (the list's
 * own control is disabled otherwise). It still handles the 409: somebody can
 * move a customer in between this list and the click, and the server's own
 * detail already names how many, so the notification passes it straight through
 * rather than rewording it.
 */
const DeleteGroupConfirmation = ({
  group,
  onDeleted,
  onDone,
}: {
  group: CustomerGroupSummary;
  /** Told which group went, so a caller filtering by it can let go (the list page's URL). */
  onDeleted?: (groupId: string) => void;
  onDone: () => void;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => deleteGroup(group.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onDeleted?.(group.id);
      onDone();
      notifications.show({ color: "teal", title: t("groupDeleted"), message: t("groupDeletedMessage") });
    },
    onError: (error) => {
      // A 409 group_in_use (a customer moved in since the list loaded) or a 404
      // (deleted in another tab): either way the row's count and its enabled
      // delete are stale, and the next click would fail the same way. The list
      // is read again so the row says what the server just did.
      queryClient.invalidateQueries({ queryKey: customerGroupsQueryOptions().queryKey });
      notifications.show({ color: "red", title: t("groupCouldNotBeDeleted"), message: error.message });
    },
  });

  return (
    <Alert color="red" title={t("deleteGroup")}>
      <Stack gap="xs">
        <Text size="sm">{t("deleteGroupConfirmation", { name: group.name })}</Text>
        <Group justify="flex-end">
          <Button size="xs" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button size="xs" color="red" loading={mutation.isPending} onClick={() => mutation.mutate()}>
            {t("deleteGroup")}
          </Button>
        </Group>
      </Stack>
    </Alert>
  );
};
