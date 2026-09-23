import { Alert, Badge, Button, Card, Divider, Group, MultiSelect, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconUserStar } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  ApiConflictError,
  type CustomerResponse,
  customerQueryOptions,
  invalidateCustomersExcept,
  syncCustomerRevision,
} from "../api/customers";
import { setCustomerOwner } from "../api/owner";
import { type CustomerTag, createTag, customerTagsQueryOptions, setCustomerTags } from "../api/tags";
import { OwnerPicker } from "../components/owner-picker";
import { TagBadge } from "../components/tag-badge";
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
 *    and answers the whole customer: that body goes straight into this query's
 *    cache (the Owner row moves with no round trip), `syncCustomerRevision`
 *    writes the fresh revision into every other cache entry that carries it
 *    BEFORE the invalidation's refetches land, or an editor opened in that
 *    window sends the revision this save just replaced, and
 *    `invalidateCustomersExcept` refreshes the rest of `["customers"]` without
 *    throwing away what the response just supplied. A 409 raises the same
 *    conflict alert and Reload the other row-editing modals use
 *    (`useCustomerReload`). The revision this card sends is read straight off
 *    the query and never copied into state: unlike a modal, this card is mounted
 *    the whole time the other editors are saving, and their
 *    `syncCustomerRevision` moves the cached revision under it — a private copy
 *    would go stale into a 409 nobody caused (`-customer-peppol-status.tsx`
 *    sends the profile's own revision the same way).
 *  - The tags are off the row: no revision to sync and no conflict to handle,
 *    but the set replace does answer the customer's tags, so those are patched
 *    into the cached customer the same way before the rest of `["customers"]` is
 *    invalidated — the chips on the list and the counts in Manage tags moved too.
 */
export const CustomerRelationshipCard = ({ customerId, canEdit }: { customerId: number; canEdit?: boolean }) => {
  const { t } = useI18n("customers");
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const queryClient = useQueryClient();
  const customerKey = customerQueryOptions(customerId).queryKey;
  const [conflict, setConflict] = useState(false);

  const reload = useCustomerReload({
    customerId,
    queryKey: customerKey,
    fetchFresh: () => queryClient.fetchQuery({ ...customerQueryOptions(customerId), staleTime: 0 }),
    revisionOf: (fresh) => fresh.revision,
    // Nothing to re-seed but the banner: `fetchQuery` has already put the fresh
    // row in this query's cache, and the revision the next save sends is read
    // from there.
    seed: () => setConflict(false),
  });

  const ownerMutation = useMutation({
    mutationFn: (ownerUserId: string | null) => setCustomerOwner(customerId, ownerUserId, customer.revision),
    onMutate: () => {
      setConflict(false);
      // A reload that failed belonged to the PREVIOUS conflict; forgetting it
      // here keeps its red line out of a conflict raised minutes later.
      reload.forget();
    },
    onSuccess: (saved) => {
      // The PUT answered the whole customer, so the row this card reads is
      // already in hand: it goes straight into the cache (the Owner row moves
      // with no round trip at all), the fresh revision is carried to every other
      // entry holding one, and the rest of ["customers"] is invalidated — but
      // not this key, which is the one thing that is already fresh.
      queryClient.setQueryData(customerKey, saved);
      syncCustomerRevision(queryClient, customerId, saved.revision);
      invalidateCustomersExcept(queryClient, customerKey);
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
                  {t("ownerInactive")}
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
            <TagBadge key={tag.id} tag={tag} />
          ))}
        </Group>
        {canEdit && <TagsEditor customerId={customerId} tags={customer.tags} />}
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
 *
 * A create the server refuses because the name is already taken (409
 * `tag_exists`) is not an error worth showing: the vocabulary is read again and
 * the tag holding that name is attached, which is what was asked for.
 */
const TagsEditor = ({ customerId, tags }: { customerId: number; tags: CustomerTag[] }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const { data: vocabulary } = useQuery(customerTagsQueryOptions());
  const [search, setSearch] = useState("");
  const selected = tags.map((tag) => tag.id);
  const customerKey = customerQueryOptions(customerId).queryKey;

  // Both writes answer the customer's tags, so both end the same way: the
  // answered set into the cached customer (chips and pills move without a round
  // trip), then everything else under ["customers"] invalidated — the list's
  // chips and Manage tags' counts moved too. A cached row is patched, never
  // invented: a tag list is not enough to make a customer out of.
  const applyTags = (answered: { tags: CustomerTag[] }) => {
    queryClient.setQueryData(customerKey, (old?: CustomerResponse) => (old ? { ...old, tags: answered.tags } : old));
    return invalidateCustomersExcept(queryClient, customerKey);
  };

  const mutation = useMutation({
    mutationFn: (tagIds: string[]) => setCustomerTags(customerId, tagIds),
    // No revision to sync: tags are off the customer row (design D2).
    onSuccess: applyTags,
    onError: (error) => notifications.show({ color: "red", title: t("tagsCouldNotBeSaved"), message: error.message }),
  });

  const createAndAttach = useMutation({
    mutationFn: async (name: string) => {
      // A Set because the name may belong to a tag the customer already
      // carries: attaching it twice is a request the API would refuse.
      const attach = (tagId: string) => setCustomerTags(customerId, [...new Set([...selected, tagId])]);
      try {
        return await attach((await createTag({ name, color: null })).id);
      } catch (error) {
        if (!(error instanceof ApiConflictError) || error.code !== "tag_exists") throw error;
        // The name is taken: someone else created it, or this vocabulary was
        // read before it existed. Either way the person asked for this customer
        // to carry a tag by that name, so the tag that holds it is attached
        // rather than the refusal shown — nobody caused a conflict here.
        // Fetched, not invalidated: an invalidation resolves even when its
        // refetch failed, which would leave nothing to look the name up in.
        const fresh = await queryClient.fetchQuery({ ...customerTagsQueryOptions(), staleTime: 0 });
        const existing = fresh.find((tag) => tag.name.toLowerCase() === name.toLowerCase());
        // No such tag after all (a rename in the same second, say): the
        // server's refusal is the honest answer.
        if (!existing) throw error;
        return await attach(existing.id);
      }
    },
    onSuccess: (saved) => {
      setSearch("");
      applyTags(saved);
    },
    onError: (error) => notifications.show({ color: "red", title: t("tagCouldNotBeSaved"), message: error.message }),
  });

  // The customer's OWN tags seed the option list, and the vocabulary widens it.
  // A MultiSelect renders the raw VALUE of a selected option its `data` does not
  // describe — a uuid here — so without this the pills read as uuids until the
  // vocabulary lands, and for good if it fails.
  const options = new Map(tags.map((tag) => [tag.id, tag.name]));
  for (const tag of vocabulary ?? []) options.set(tag.id, tag.name);

  const term = search.trim();
  const exists = [...options.values()].some((name) => name.toLowerCase() === term.toLowerCase());
  const data = [
    ...[...options].map(([id, name]) => ({ value: id, label: name })),
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
        // The term has served its purpose: left behind it would keep the list
        // narrowed to it while the pill for what was just picked is right there.
        setSearch("");
        mutation.mutate(next);
      }}
    />
  );
};
