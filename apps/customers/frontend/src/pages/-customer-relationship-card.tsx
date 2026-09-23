import { Alert, Badge, Button, Card, Divider, Group, MultiSelect, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconUserStar } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ApiConflictError, customerQueryOptions, syncCustomerRevision } from "../api/customers";
import { setCustomerOwner } from "../api/owner";
import { createTag, customerTagsQueryOptions, setCustomerTags } from "../api/tags";
import { OwnerPicker } from "../components/owner-picker";
import "../i18n";
import { useCustomerReload } from "../lib/customer-reload";

/**
 * The customer page's "Relationship" card (owner and tags design D3): who owns
 * the relationship, and how the customer is classified. `canEdit` comes from
 * the host, which reads the caller's `customers:update` permission — this
 * package never fetches permissions itself.
 *
 * Both values ride on the customer read this page already made (the owner is a
 * column, the tags come decorated onto the same response), so this card issues
 * no query of its own for them — only the tag VOCABULARY, which is
 * installation-wide, and the user search, which lives inside the picker.
 *
 * The two writes reload differently, and deliberately:
 *
 *  - The owner is a column on the customer row, so its PUT is revision-guarded
 *    and answers the whole customer: `syncCustomerRevision` writes the fresh
 *    revision into every cache entry that carries it BEFORE the invalidation's
 *    refetches land, or an editor opened in that window sends the revision this
 *    save just replaced. A 409 raises the same conflict alert and Reload the
 *    other row-editing modals use (`useCustomerReload`).
 *  - The tags are off the row: no revision, nothing to sync, no conflict to
 *    handle — a plain invalidation of `["customers"]`, because the chips on the
 *    list and the counts in Manage tags both moved.
 */
export const CustomerRelationshipCard = ({ customerId, canEdit }: { customerId: number; canEdit?: boolean }) => {
  const { t } = useI18n("customers");
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const queryClient = useQueryClient();
  const [conflict, setConflict] = useState(false);
  const [revision, setRevision] = useState(customer.revision);

  const reload = useCustomerReload({
    customerId,
    queryKey: customerQueryOptions(customerId).queryKey,
    fetchFresh: () => queryClient.fetchQuery({ ...customerQueryOptions(customerId), staleTime: 0 }),
    revisionOf: (fresh) => fresh.revision,
    seed: (fresh) => {
      setRevision(fresh.revision);
      setConflict(false);
    },
  });

  const ownerMutation = useMutation({
    mutationFn: (ownerUserId: string | null) => setCustomerOwner(customerId, ownerUserId, revision),
    onSuccess: (saved) => {
      syncCustomerRevision(queryClient, customerId, saved.revision);
      setRevision(saved.revision);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      setConflict(false);
      notifications.show({ color: "teal", title: t("ownerUpdated"), message: t("ownerUpdatedMessage") });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        setConflict(true);
        return;
      }
      notifications.show({ color: "red", title: t("ownerCouldNotBeSaved"), message: error.message });
    },
  });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="lg">
        <Group gap="xs">
          <IconUserStar size={18} stroke={1.5} />
          <Text fw={600} component="h3">
            {t("relationship")}
          </Text>
        </Group>

        {conflict && (
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

        <Group gap="xs" wrap="nowrap" align="center">
          <Text size="sm" c="dimmed" miw={64}>
            {t("owner")}
          </Text>
          {customer.owner ? (
            <Group gap="xs" wrap="nowrap">
              <Text size="sm">{customer.owner.displayName}</Text>
              {!customer.owner.active && (
                <Badge variant="light" color="gray" size="sm">
                  {t("inactive")}
                </Badge>
              )}
            </Group>
          ) : (
            <Text size="sm">—</Text>
          )}
        </Group>
        {canEdit && (
          <OwnerPicker
            value={customer.owner?.userId ?? null}
            selected={customer.owner}
            disabled={ownerMutation.isPending}
            onChange={(value) => ownerMutation.mutate(value)}
          />
        )}

        <Divider />

        <Group gap="xs" wrap="wrap" align="center">
          <Text size="sm" c="dimmed" miw={64}>
            {t("tags")}
          </Text>
          {customer.tags.length === 0 && <Text size="sm">—</Text>}
          {customer.tags.map((tag) => (
            <Badge key={tag.id} variant="light" color={tag.color ?? "gray"} size="sm">
              {tag.name}
            </Badge>
          ))}
        </Group>
        {canEdit && <TagsEditor customerId={customerId} selected={customer.tags.map((tag) => tag.id)} />}
      </Stack>
    </Card>
  );
};

/**
 * The tag multi-select, with create-on-the-fly (design D3): typing a name no
 * tag has offers *Create "x"*, which POSTs the tag and then replaces the
 * customer's set including it — two calls, in that order, because the set
 * replace can only name ids that exist.
 *
 * Every change sends the WHOLE set, not a delta: that is what the endpoint
 * means, and it is why two people editing the same customer's tags are
 * last-wins rather than silently merged.
 */
const TagsEditor = ({ customerId, selected }: { customerId: number; selected: string[] }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const { data: vocabulary } = useQuery(customerTagsQueryOptions());
  const [search, setSearch] = useState("");

  const mutation = useMutation({
    mutationFn: (tagIds: string[]) => setCustomerTags(customerId, tagIds),
    onSuccess: () => {
      // No revision to sync: tags are off the customer row (design D2), so the
      // only thing that moved is what the chips and the counts say.
      queryClient.invalidateQueries({ queryKey: ["customers"] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("tagsCouldNotBeSaved"), message: error.message }),
  });

  const createAndAttach = useMutation({
    mutationFn: async (name: string) => {
      const created = await createTag({ name, color: null });
      return setCustomerTags(customerId, [...selected, created.id]);
    },
    onSuccess: () => {
      setSearch("");
      queryClient.invalidateQueries({ queryKey: ["customers"] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("tagCouldNotBeSaved"), message: error.message }),
  });

  const term = search.trim();
  const exists = (vocabulary ?? []).some((tag) => tag.name.toLowerCase() === term.toLowerCase());
  const data = [
    ...(vocabulary ?? []).map((tag) => ({ value: tag.id, label: tag.name })),
    // The create entry is an option rather than a button beside the input, so
    // one keyboard path does both: type, arrow down, enter.
    ...(term && !exists ? [{ value: `create:${term}`, label: t("createTagNamed", { name: term }) }] : []),
  ];

  return (
    <MultiSelect
      label={t("tags")}
      placeholder={t("searchTags")}
      searchable
      data={data}
      value={selected}
      searchValue={search}
      onSearchChange={setSearch}
      disabled={mutation.isPending || createAndAttach.isPending}
      onChange={(next) => {
        const creating = next.find((value) => value.startsWith("create:"));
        if (creating) {
          createAndAttach.mutate(creating.slice("create:".length));
          return;
        }
        mutation.mutate(next);
      }}
    />
  );
};
