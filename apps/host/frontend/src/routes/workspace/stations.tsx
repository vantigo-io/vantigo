import {
  Alert,
  Badge,
  Button,
  Code,
  Menu,
  Modal,
  MultiSelect,
  NumberInput,
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
  issuePairingCode,
  listStations,
  registerStation,
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
  networks: string;
  workCenters: string[];
};

const emptyDraft: Draft = {
  id: null,
  name: "",
  idleMinutes: 5,
  requirePinWithBadge: true,
  networks: "",
  workCenters: [],
};

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
  const [draft, setDraft] = useState<Draft | null>(null);
  const [code, setCode] = useState<StationPairing | null>(null);
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
      };
      let id = d.id;
      let pairing: StationPairing | null = null;
      if (id) await updateStation(id, body);
      else {
        pairing = await registerStation(body);
        id = pairing.station.id;
      }
      if (centers.data) await setStationWorkCenters(id, d.workCenters.map(Number));
      return pairing;
    },
    onSuccess: async (pairing) => {
      setDraft(null);
      if (pairing) setCode(pairing);
      else notifications.show({ color: "teal", title: t("stations.saved"), message: "" });
      await refresh();
      await queryClient.invalidateQueries({ queryKey: ["manufacturing", "stations"] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("stations.couldNotSave"), message: (error as Error).message }),
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
    let workCenters: string[] = [];
    if (centers.data) {
      const ids = await queryClient.fetchQuery(stationWorkCentersQueryOptions(station.id)).catch(() => [] as number[]);
      workCenters = ids.map(String);
    }
    setDraft({
      id: station.id,
      name: station.name,
      idleMinutes: station.idleMinutes,
      requirePinWithBadge: station.requirePinWithBadge,
      networks: (station.allowedNetworks ?? []).join("\n"),
      workCenters,
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
                      <Badge color={stateColor[station.state]} variant="light">
                        {t(`stations.state_${station.state}`)}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      {station.lastSeenAt
                        ? `${formatters.formatDate(station.lastSeenAt, { dateStyle: "medium", timeStyle: "short" })}${
                            station.lastSeenIp ? ` · ${station.lastSeenIp}` : ""
                          }`
                        : t("stations.never")}
                    </Table.Td>
                    <Table.Td>
                      {station.activeOperators.map((operator) => operator.displayName).join(", ") || "—"}
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
              <Textarea
                label={t("stations.allowedNetworks")}
                description={t("stations.allowedNetworksHint")}
                autosize
                minRows={2}
                value={draft.networks}
                onChange={(event) => setDraft({ ...draft, networks: event.currentTarget.value })}
              />
              {centers.data && (
                <MultiSelect
                  label={t("stations.workCenters")}
                  description={t("stations.workCentersHint")}
                  searchable
                  data={centers.data.map((center) => ({
                    value: String(center.id),
                    label: `${center.code} · ${center.name}`,
                  }))}
                  value={draft.workCenters}
                  onChange={(workCenters) => setDraft({ ...draft, workCenters })}
                />
              )}
              <Button type="submit" loading={save.isPending} disabled={!draft.name.trim()}>
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
