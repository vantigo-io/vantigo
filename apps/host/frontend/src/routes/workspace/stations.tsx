import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  Menu,
  Modal,
  MultiSelect,
  NumberInput,
  PasswordInput,
  Stack,
  Switch,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconDots, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { appUrl, ContentSkeleton, EmptyState, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import {
  setStationWorkCenters,
  stationWorkCentersQueryOptions,
  workCentersQueryOptions,
} from "@vantigo/manufacturing-ui";
import { useState } from "react";
import { getAuthorizationMe } from "../../api/authorization";
import {
  endOperatorSession,
  issuePairingCode,
  listStations,
  registerStation,
  resetBackoff,
  revokeStation,
  type Station,
  type StationPairing,
  stationsQueryKey,
  updateStation,
} from "../../api/stations";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import { ModuleNotFound, moduleDisabledNotFound } from "../-app-layout";

const stateColor: Record<Station["state"], string> = {
  pairing: "blue",
  paired: "teal",
  expired: "orange",
  unpaired: "gray",
  revoked: "red",
};

type Draft = {
  id: string | null;
  name: string;
  idleMinutes: number;
  requirePinWithBadge: boolean;
  /** Whether the station took the PIN with a badge when the form opened: turning that off asks for the password. */
  requiredPin: boolean;
  networks: string;
  workCenters: string[];
  /** The work centers as read, to send only a change; null when they could not be read. */
  readCenters: string[] | null;
  currentPassword: string;
};

const emptyDraft: Draft = {
  id: null,
  name: "",
  idleMinutes: 5,
  requirePinWithBadge: true,
  requiredPin: true,
  networks: "",
  workCenters: [],
  readCenters: [],
  currentPassword: "",
};

/** The same work centers, in any order. */
const sameCenters = (a: string[], b: string[]) =>
  a.length === b.length && [...a].sort().join() === [...b].sort().join();

/** A refusal's code, whichever shape the client threw it in. */
const codeOf = (error: unknown) => (error as { code?: string } | null)?.code;

/**
 * Workspace › Stations (manufacturing phase 2 M129, M139): the shared
 * shop-floor tablets — register one and hand it its one-time pairing code,
 * change its name, idle timeout, badge rule, network allowlist and the work
 * centers it serves, issue a new code, revoke it. Each row says who is signed
 * in at it, when it was last active and whether failed sign-ins alerted the
 * administrators.
 */
const StationsPage = () => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const stations = useQuery({ queryKey: stationsQueryKey, queryFn: listStations });
  const centers = useQuery({ ...workCentersQueryOptions({ status: "all" }), retry: false });
  const me = useQuery({ queryKey: ["authorization", "me"], queryFn: getAuthorizationMe });
  // The work centers are Manufacturing's, set with manufacturing:boms-manage:
  // without it the field is shown, not changed.
  const manageCenters = hasPermissions(me.data?.permissions ?? [], ["manufacturing:boms-manage"]);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [code, setCode] = useState<StationPairing | null>(null);
  // When the page opened: a pairing code still waiting is judged against it.
  const [openedAt] = useState(() => Date.now());
  const refresh = () => queryClient.invalidateQueries({ queryKey: stationsQueryKey });

  const save = useMutation({
    mutationFn: async (d: Draft) => {
      const networks = d.networks
        .split(/[\s,]+/)
        .map((value) => value.trim())
        .filter(Boolean);
      const body = {
        name: d.name.trim(),
        idleMinutes: d.idleMinutes,
        requirePinWithBadge: d.requirePinWithBadge,
        allowedNetworks: networks.length > 0 ? networks : null,
        ...(d.currentPassword ? { currentPassword: d.currentPassword } : {}),
      };
      let id = d.id;
      let pairing: StationPairing | null = null;
      if (id) await updateStation(id, body);
      else {
        pairing = await registerStation(body);
        id = pairing.station.id;
      }
      // The centers go only when they changed and may be changed; the station
      // is saved, and a new one's code shown, whether they are or not.
      let centersFailed: Error | null = null;
      if (manageCenters && d.readCenters !== null && !sameCenters(d.readCenters, d.workCenters))
        await setStationWorkCenters(id, d.workCenters.map(Number)).catch((error: Error) => {
          centersFailed = error;
        });
      return { pairing, centersFailed: centersFailed as Error | null };
    },
    onSuccess: async ({ pairing, centersFailed }) => {
      setDraft(null);
      if (pairing) setCode(pairing);
      else notifications.show({ color: "teal", title: t("stations.saved"), message: "" });
      if (centersFailed)
        notifications.show({ color: "red", title: t("stations.centersNotSaved"), message: centersFailed.message });
      await refresh();
      await queryClient.invalidateQueries({ queryKey: ["manufacturing", "stations"] });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("stations.couldNotSave"),
        message:
          codeOf(error) === "reauthentication_required"
            ? t("stations.reauth")
            : codeOf(error) === "invalid_credentials"
              ? t("stations.pinWrongPassword")
              : (error as Error).message,
      }),
  });
  const endSession = useMutation({
    mutationFn: ({ stationId, sessionId }: { stationId: string; sessionId: string }) =>
      endOperatorSession(stationId, sessionId),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("stations.sessionEnded"), message: "" });
      await refresh();
    },
  });
  const reset = useMutation({
    mutationFn: ({ stationId, userId }: { stationId: string; userId: string }) => resetBackoff(stationId, userId),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("stations.backoffReset"), message: "" });
      await refresh();
    },
  });
  const newCode = useMutation({
    mutationFn: issuePairingCode,
    onSuccess: async (pairing) => {
      setCode(pairing);
      await refresh();
    },
  });
  const revoke = useMutation({
    mutationFn: revokeStation,
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("stations.revoked"), message: "" });
      await refresh();
    },
  });

  const edit = async (station: Station) => {
    // Centers that could not be read are not taken for none: saving is
    // blocked until they can be.
    let readCenters: string[] | null = [];
    if (centers.data) {
      const ids = await queryClient.fetchQuery(stationWorkCentersQueryOptions(station.id)).catch(() => null);
      readCenters = ids ? ids.map(String) : null;
    }
    setDraft({
      id: station.id,
      name: station.name,
      idleMinutes: station.idleMinutes,
      requirePinWithBadge: station.requirePinWithBadge,
      requiredPin: station.requirePinWithBadge,
      networks: (station.allowedNetworks ?? []).join("\n"),
      workCenters: readCenters ?? [],
      readCenters,
      currentPassword: "",
    });
  };
  const confirmRevoke = (station: Station) =>
    modals.openConfirmModal({
      title: t("stations.revokeTitle", { name: station.name }),
      children: <Text size="sm">{t("stations.revokeBody")}</Text>,
      labels: { confirm: t("stations.revoke"), cancel: t("stations.close") },
      confirmProps: { color: "red" },
      onConfirm: () => revoke.mutate(station.id),
    });

  return (
    <Stack gap="xl">
      <PageHeader
        title={t("stations.title")}
        description={t("stations.description")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setDraft(emptyDraft)}>
            {t("stations.register")}
          </Button>
        }
      />
      <PageSection>
        {stations.isPending ? (
          <ContentSkeleton rows={3} />
        ) : (stations.data ?? []).length === 0 ? (
          <EmptyState title={t("stations.empty")} />
        ) : (
          <Table.ScrollContainer minWidth={720}>
            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("stations.name")}</Table.Th>
                  <Table.Th>{t("stations.state")}</Table.Th>
                  <Table.Th>{t("stations.lastSeen")}</Table.Th>
                  <Table.Th>{t("stations.operators")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(stations.data ?? []).map((station) => (
                  <Table.Tr key={station.id}>
                    <Table.Td>
                      <Stack gap={2}>
                        <Text fw={600}>{station.name}</Text>
                        {station.alertedAt && (
                          <Badge color="red" variant="light" leftSection={<IconAlertTriangle size={12} />}>
                            {t("stations.alerted")}
                          </Badge>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={4}>
                        <Badge color={stateColor[station.state]} variant="light">
                          {t(`stations.state_${station.state}`)}
                        </Badge>
                        {station.state === "paired" &&
                          station.pairingExpiresAt &&
                          Date.parse(station.pairingExpiresAt) > openedAt && (
                            <Badge color="blue" variant="outline">
                              {t("stations.repairPending")}
                            </Badge>
                          )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      {station.lastSeenAt
                        ? `${formatters.formatDate(station.lastSeenAt, { dateStyle: "medium", timeStyle: "short" })}${
                            station.lastSeenIp ? ` · ${station.lastSeenIp}` : ""
                          }`
                        : t("stations.never")}
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={4}>
                        {station.activeOperators.length === 0 && <Text size="sm">—</Text>}
                        {station.activeOperators.map((operator) => (
                          <Group key={operator.sessionId} gap={4} wrap="nowrap">
                            <Text size="sm">{operator.displayName}</Text>
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              color="red"
                              loading={endSession.isPending && endSession.variables?.sessionId === operator.sessionId}
                              onClick={() =>
                                endSession.mutate({ stationId: station.id, sessionId: operator.sessionId })
                              }
                            >
                              {t("stations.endSession")}
                            </Button>
                          </Group>
                        ))}
                        {station.backoff.map((run) => (
                          <Group key={run.userId} gap={4} wrap="nowrap">
                            <Badge color="orange" variant="light">
                              {t("stations.backoffRun", { name: run.displayName, count: run.failures })}
                            </Badge>
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              loading={reset.isPending && reset.variables?.userId === run.userId}
                              onClick={() => reset.mutate({ stationId: station.id, userId: run.userId })}
                            >
                              {t("stations.resetBackoff")}
                            </Button>
                          </Group>
                        ))}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Menu position="bottom-end">
                        <Menu.Target>
                          <Button variant="subtle" size="compact-sm" aria-label={station.name}>
                            <IconDots size={16} />
                          </Button>
                        </Menu.Target>
                        <Menu.Dropdown>
                          <Menu.Item onClick={() => void edit(station)}>{t("stations.edit")}</Menu.Item>
                          <Menu.Item onClick={() => newCode.mutate(station.id)}>{t("stations.newCode")}</Menu.Item>
                          {station.state !== "revoked" && (
                            <Menu.Item color="red" onClick={() => confirmRevoke(station)}>
                              {t("stations.revoke")}
                            </Menu.Item>
                          )}
                        </Menu.Dropdown>
                      </Menu>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </PageSection>
      <Modal
        opened={draft !== null}
        onClose={() => setDraft(null)}
        title={draft?.id ? t("stations.edit") : t("stations.register")}
        centered
      >
        {draft && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              save.mutate(draft);
            }}
          >
            <Stack>
              <TextInput
                label={t("stations.name")}
                required
                maxLength={100}
                value={draft.name}
                onChange={(event) => setDraft({ ...draft, name: event.currentTarget.value })}
              />
              <NumberInput
                label={t("stations.idleMinutes")}
                description={t("stations.idleMinutesHint")}
                min={1}
                max={60}
                value={draft.idleMinutes}
                onChange={(value) => setDraft({ ...draft, idleMinutes: Number(value) || 5 })}
              />
              <Switch
                label={t("stations.requirePinWithBadge")}
                description={t("stations.requirePinWithBadgeHint")}
                checked={draft.requirePinWithBadge}
                onChange={(event) => setDraft({ ...draft, requirePinWithBadge: event.currentTarget.checked })}
              />
              {draft.requiredPin && !draft.requirePinWithBadge && (
                <PasswordInput
                  label={t("stations.currentPassword")}
                  description={t("stations.badgeOnlyPasswordHint")}
                  value={draft.currentPassword}
                  onChange={(event) => setDraft({ ...draft, currentPassword: event.currentTarget.value })}
                />
              )}
              <Textarea
                label={t("stations.allowedNetworks")}
                description={t("stations.allowedNetworksHint")}
                autosize
                minRows={2}
                value={draft.networks}
                onChange={(event) => setDraft({ ...draft, networks: event.currentTarget.value })}
              />
              {centers.data && draft.readCenters === null && (
                <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("stations.centersNotRead")}>
                  {t("stations.centersNotReadHint")}
                </Alert>
              )}
              {centers.data && draft.readCenters !== null && (
                <MultiSelect
                  label={t("stations.workCenters")}
                  description={manageCenters ? t("stations.workCentersHint") : t("stations.workCentersReadOnly")}
                  searchable
                  disabled={!manageCenters}
                  data={centers.data.map((center) => ({
                    value: String(center.id),
                    label: `${center.code} · ${center.name}`,
                  }))}
                  value={draft.workCenters}
                  onChange={(workCenters) => setDraft({ ...draft, workCenters })}
                />
              )}
              <Button
                type="submit"
                loading={save.isPending}
                disabled={!draft.name.trim() || (centers.data !== undefined && draft.readCenters === null)}
              >
                {t("stations.save")}
              </Button>
            </Stack>
          </form>
        )}
      </Modal>
      <Modal
        opened={code !== null}
        onClose={() => setCode(null)}
        title={code ? t("stations.codeTitle", { name: code.station.name }) : ""}
        centered
      >
        {code && (
          <Stack>
            <Title order={1} ta="center" ff="monospace">
              {`${code.pairingCode.slice(0, 4)}-${code.pairingCode.slice(4)}`}
            </Title>
            <Alert color="blue">
              {t("stations.codeBody", {
                address: appUrl("/station"),
                until: formatters.formatDate(code.pairingExpiresAt, { timeStyle: "short" }),
              })}
            </Alert>
            <Code>{appUrl("/station")}</Code>
            <Button onClick={() => setCode(null)}>{t("stations.close")}</Button>
          </Stack>
        )}
      </Modal>
    </Stack>
  );
};

export const Route = createFileRoute("/workspace/stations")({
  beforeLoad: async ({ context }) => {
    if (!enabledModuleKeys().includes("manufacturing")) throw moduleDisabledNotFound("manufacturing");
    const access = await context.queryClient.fetchQuery({
      queryKey: ["authorization", "me"],
      queryFn: getAuthorizationMe,
    });
    if (!hasPermissions(access.permissions, ["identity:manage"])) throw redirect({ to: "/" });
  },
  notFoundComponent: ({ data }) => <ModuleNotFound labelKey="navigation.stations" data={data} />,
  component: StationsPage,
});
