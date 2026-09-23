import { Alert, Badge, Button, Card, CloseButton, Group, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconBuildingBank, IconRefresh } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type LegalIdentityResponse, legalIdentityQueryOptions, upsertLegalIdentity } from "../api/customers";
import {
  ApiConflictError,
  type CustomerRegistryRecord,
  customerRegistryRecordQueryOptions,
  NO_REGISTRY_IDENTITY_CODE,
  refreshRegistryRecord,
} from "../api/registry";
import { CustomerRegistryFields } from "./-customer-registry-fields";
import "../i18n";

/**
 * What a refresh left behind that the record itself cannot say (design D2):
 * an organisation number the register does not know and an entity removed
 * from open data both store nothing — the second one deletes the record on
 * file — so the answer lives here until the next click. `no_identity` and
 * `unavailable` are the 409 and the 502.
 */
type RefreshNote = "unknown" | "removed" | "no_identity" | "unavailable";

/**
 * The customer page's "Registry" card (design D5): what Enhetsregisteret says
 * about this customer, with its provenance, the status flags that matter in
 * red, a Refresh that re-reads the register, and the notice that the register
 * calls the company something else. Rendered by the Overview tab only for a
 * customer whose legal identity is a Norwegian business and only for a caller
 * with `customers:legal-identity-view` — the record repeats that identity's
 * organisation number and is that permission's to show.
 *
 * `canManageIdentity` comes from the host's `customers:legal-identity-manage`
 * check, the permission both writes behind this card need; this package never
 * fetches permissions itself. A plain `useQuery` with a skeleton and an error
 * branch, not `useSuspenseQuery`: a failing registry GET must cost this card
 * alone, not the whole tab — the same choice the Billing card makes.
 */
export const CustomerRegistryCard = ({
  customerId,
  canManageIdentity,
}: {
  customerId: number;
  canManageIdentity?: boolean;
}) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const recordKey = customerRegistryRecordQueryOptions(customerId).queryKey;
  const { data: record, isPending, isError, refetch } = useQuery(customerRegistryRecordQueryOptions(customerId));
  // The identity the user asserted, which the rename notice compares against
  // and the rename write keeps every other field of. Already fetched by the
  // page header under this same key, so this costs no second round trip.
  const { data: identity } = useQuery(legalIdentityQueryOptions(customerId));

  const [changeCount, setChangeCount] = useState<number | null>(null);
  const [note, setNote] = useState<RefreshNote | null>(null);

  const refresh = useMutation({
    mutationFn: () => refreshRegistryRecord(customerId),
    // Each click answers for itself: whatever the last one left on screen is
    // about a reading the register has just replaced.
    onMutate: () => {
      setChangeCount(null);
      setNote(null);
    },
    onSuccess: (result) => {
      // "found" and "deleted" leave no note: the record itself, refetched
      // below, already says what changed. A status a future build adds and
      // this one has no words for falls through the same way, silently.
      if (result.status === "unknown") setNote("unknown");
      if (result.status === "removed") setNote("removed");
      if (result.changes.length > 0) setChangeCount(result.changes.length);
      // The server has already stored (or deleted) the record, so the card
      // reads it again rather than trusting this POST's own copy — one place
      // the record comes from, whatever wrote it. Returned, not fired and
      // forgotten, so Refresh stays pending until the card is up to date.
      const refreshed = queryClient.invalidateQueries({ queryKey: recordKey });
      if (result.changes.length === 0) return refreshed;
      // A diff is written as one `registry.change` event (design D4), and the
      // timeline is on this same page. Never `["customers", id]` itself: the
      // registry record lives off the customer row, whose revision and
      // everything keyed on it are untouched by a refresh.
      return Promise.all([
        refreshed,
        queryClient.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] }),
      ]);
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && error.code === NO_REGISTRY_IDENTITY_CODE) {
        setNote("no_identity");
        return;
      }
      if ((error as { status?: number }).status === 502) {
        setNote("unavailable");
        return;
      }
      notifications.show({ color: "red", title: t("registryRefreshFailed"), message: error.message });
    },
  });

  const badges = record ? statusBadges(t, formatters.formatDate, record) : [];
  if (note === "removed") badges.push({ key: "removed", label: t("registryBadgeRemoved") });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="lg">
        <Group justify="space-between">
          <Group gap="xs">
            <IconBuildingBank size={18} stroke={1.5} />
            <Text fw={600} component="h3">
              {t("registry")}
            </Text>
            {badges.map((badge) => (
              <Badge key={badge.key} color="red" variant="light">
                {badge.label}
              </Badge>
            ))}
          </Group>
          {canManageIdentity && (
            <Button
              variant="light"
              size="xs"
              leftSection={<IconRefresh size={14} />}
              loading={refresh.isPending}
              onClick={() => refresh.mutate()}
            >
              {t("registryRefresh")}
            </Button>
          )}
        </Group>

        {/* Compared trimmed, the same rule the server's own write applies
            (design D4): leading/trailing whitespace on either side is not a
            difference worth a notice, and never anything worth writing. */}
        {record && identity && record.name.trim() !== identity.name.trim() && (
          <RegistryRenameNotice
            customerId={customerId}
            identity={identity}
            registryName={record.name}
            canManageIdentity={canManageIdentity}
          />
        )}

        {isPending ? (
          <ContentSkeleton rows={4} rowHeight={24} />
        ) : isError ? (
          // A failed load is not "no record": the same shape the addresses
          // section and the timeline give their own failures.
          <Stack align="center" py="md">
            <Text c="red">{t("failedLoadRegistryRecord")}</Text>
            <Button variant="light" onClick={() => refetch()}>
              {t("tryAgain")}
            </Button>
          </Stack>
        ) : record ? (
          <Stack gap="xs">
            <CustomerRegistryFields record={record} />
            {/* The register reported a change this record does not have yet
                (design D2): either a background refresh failed, or the feed has
                only just said so and the sweep has not caught up. Not dimmed,
                unlike the provenance line below it — it is the one thing on this
                card that is actionable, and Refresh is right there in the
                header. Both values are instants, so both format in local time. */}
            {isBehindTheRegistry(record) && (
              <Text size="sm">
                {t("registryUpdatedHintLine", {
                  reported: formatters.formatDate(record.registryUpdatedHint as string, {
                    dateStyle: "medium",
                    timeStyle: "short",
                  }),
                  fetched: formatters.formatDate(record.fetchedAt, { dateStyle: "medium", timeStyle: "short" }),
                })}
              </Text>
            )}
            {/* Date and time: two refreshes on the same day would otherwise
                read as one, and seeing the reading move is the point. */}
            <Text size="xs" c="dimmed">
              {t("registryFetchedFrom", {
                date: formatters.formatDate(record.fetchedAt, { dateStyle: "medium", timeStyle: "short" }),
              })}
            </Text>
          </Stack>
        ) : note === "removed" ? null : (
          // A removed entity's own note (below) already says why there is
          // nothing here — this line would otherwise repeat "not fetched yet"
          // right beside it, which is not the reason.
          <Text size="sm" c="dimmed">
            {t("registryNotFetched")}
          </Text>
        )}

        {/* What the last click found, announced rather than merely drawn.
            Mounted whenever Refresh itself is offered — not only once there is
            something to say — so a screen reader already has the region and a
            click's result is announced rather than merely drawn (the same
            choice `-customer-peppol-status.tsx` makes for its own action). */}
        {(canManageIdentity || changeCount !== null || note !== null) && (
          <Stack gap="xs" role="status">
            {changeCount !== null && (
              <Group gap="xs">
                <Text size="sm">{t("registryChangesSeeTimeline", { count: changeCount })}</Text>
                <CloseButton size="sm" aria-label={t("dismiss")} onClick={() => setChangeCount(null)} />
              </Group>
            )}
            {note === "unknown" && (
              <Text size="sm" c="dimmed">
                {t("registryUnknownOrganisation")}
              </Text>
            )}
            {note === "removed" && (
              <Text size="sm" c="dimmed">
                {t("registryRemovedNote")}
              </Text>
            )}
            {note === "no_identity" && (
              <Text size="sm" c="dimmed">
                {t("registryNoIdentity")}
              </Text>
            )}
            {note === "unavailable" && (
              <Text size="sm" c="red">
                {t("registryUnavailable")}
              </Text>
            )}
          </Stack>
        )}
      </Stack>
    </Card>
  );
};

/**
 * The statuses a person should act on, in red beside the card's title
 * (design D5). Bankruptcy and the two liquidations are separate facts in the
 * register and stay separate here; a deletion carries the date it happened,
 * since "deleted" without a date says nothing about whether it is news.
 */
const statusBadges = (
  t: (key: string, options?: Record<string, unknown>) => string,
  formatDate: (value: string, options?: Intl.DateTimeFormatOptions) => string,
  record: CustomerRegistryRecord,
) => {
  const badges: { key: string; label: string }[] = [];
  if (record.bankrupt) badges.push({ key: "bankrupt", label: t("registryBadgeBankrupt") });
  if (record.underLiquidation) badges.push({ key: "liquidation", label: t("registryBadgeUnderLiquidation") });
  if (record.underForcedLiquidation) {
    badges.push({ key: "forced-liquidation", label: t("registryBadgeUnderForcedLiquidation") });
  }
  if (record.deletedOn) {
    // A date-only value, read as UTC (see `-customer-registry-fields.tsx`'s
    // `foundedOn`): otherwise a deletion on 1 August reads 31 July west of it.
    badges.push({
      key: "deleted",
      label: t("registryBadgeDeleted", {
        date: formatDate(record.deletedOn, { dateStyle: "medium", timeZone: "UTC" }),
      }),
    });
  }
  return badges;
};

/**
 * Whether the register has reported a change this record does not have yet
 * (design D2): the hint is written before a refresh is attempted, so a hint
 * newer than `fetchedAt` is exactly "the last refresh did not catch up".
 * Compared as instants, not as strings — the two values come from different
 * writes and need not share a format.
 */
const isBehindTheRegistry = (record: CustomerRegistryRecord) =>
  record.registryUpdatedHint !== null &&
  new Date(record.registryUpdatedHint).getTime() > new Date(record.fetchedAt).getTime();

/**
 * The register calls the company something else (design D4, D5). The legal
 * identity keeps the name it was given — the name at the time of the pick —
 * until someone says otherwise, so this reports the difference and offers the
 * one write that settles it: the same identity with the registry's name.
 */
const RegistryRenameNotice = ({
  customerId,
  identity,
  registryName,
  canManageIdentity,
}: {
  customerId: number;
  identity: LegalIdentityResponse;
  registryName: string;
  canManageIdentity?: boolean;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();

  const rename = useMutation({
    mutationFn: () =>
      upsertLegalIdentity(customerId, {
        country: identity.country,
        type: identity.type,
        id: identity.id,
        // Trimmed the same way the comparison above is: the server's own
        // write trims the name, and sending it untrimmed would put the two
        // right back out of step the moment this write is read again.
        name: registryName.trim(),
        source: identity.source,
      }),
    onSuccess: (saved) => {
      // The PUT answers the identity, not the customer row — whose revision
      // it nonetheless bumps — so there is no fresh revision to hand around
      // (`syncCustomerRevision`'s case) and everything keyed on the customer
      // is re-read instead. The broad prefix covers the identity itself and
      // this card's own record query, which repeats the name.
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({
        color: "teal",
        title: t("registryLegalNameUpdated"),
        message: t("registryLegalNameUpdatedMessage", { name: saved.name }),
      });
    },
    // A 409 cannot be the duplicate-identity conflict here: the write changes
    // nothing but the name, and the identity it names is this customer's own.
    onError: (error) => {
      notifications.show({ color: "red", title: t("registryLegalNameCouldNotBeUpdated"), message: error.message });
    },
  });

  return (
    <Alert color="yellow" title={t("registryNameDiffersTitle")}>
      <Stack gap="xs">
        <Text size="sm">{t("registryNameDiffers", { registryName, legalName: identity.name })}</Text>
        {canManageIdentity && (
          <Group justify="flex-end">
            <Button size="xs" variant="light" color="yellow" loading={rename.isPending} onClick={() => rename.mutate()}>
              {t("registryUpdateLegalName")}
            </Button>
          </Group>
        )}
      </Stack>
    </Alert>
  );
};
