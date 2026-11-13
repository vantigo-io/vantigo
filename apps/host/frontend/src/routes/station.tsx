import {
  Alert,
  Avatar,
  Badge,
  Button,
  Card,
  Center,
  Group,
  Loader,
  Modal,
  PinInput,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconLogout, IconUserPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { LiveProvider, useI18n } from "@vantigo/frontend-shell";
import { MANUFACTURING_QUERY_KEY, StationShopFloor, setStationOperator } from "@vantigo/manufacturing-ui";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { signOut } from "../api/auth";
import {
  listStationOperators,
  pairStation,
  readStation,
  type StationOperatorCandidate,
  type StationOperatorSession,
  type StationSelf,
  switchIn,
  switchOut,
} from "../api/stations";
import "../i18n";

/** Why a station request was refused: its code and status. */
const refusalOf = (error: unknown) => {
  const failed = error as { status?: number; code?: string; message?: string } | null;
  return { status: failed?.status, code: failed?.code };
};

const stationQueryKey = ["station", "self"] as const;

/** An operator signed in at this station, as this page holds them: in memory only (M133). */
type HeldOperator = { sessionId: string; token: string; displayName: string; userId: string; heldAt: number };

/** How long before an idle session ends the warning shows (M140). */
const IDLE_WARNING_MS = 30_000;

/**
 * The shared shop-floor station (manufacturing phase 2 M140): outside the app
 * layout, as sign-in is. Unpaired, the pairing form; paired, a bar with the
 * station's name and its operators' chips, Switch in with the people who may
 * and a PIN pad or a badge scan, and below it the shop floor for the operator
 * whose chip is chosen — every request with that operator's token, which this
 * page holds in memory only: a reload asks the operators to switch in again.
 */
const StationPage = () => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const station = useQuery({ queryKey: stationQueryKey, queryFn: readStation, retry: false });
  const [held, setHeld] = useState<Record<string, HeldOperator>>({});
  const [active, setActive] = useState<string | null>(null);

  const forget = useCallback(
    (sessionId: string) => {
      setHeld((current) => Object.fromEntries(Object.entries(current).filter(([key]) => key !== sessionId)));
      setActive((current) => (current === sessionId ? null : current));
      void queryClient.invalidateQueries({ queryKey: stationQueryKey });
    },
    [queryClient],
  );

  // The manufacturing pages' requests go out as the active operator; a 401 is
  // the station's: the operator signed out, or the station itself unpaired.
  // Installed while rendering, so before the shop floor's first request, again
  // after a remount, and cleared when the page goes.
  const signedOut = useCallback(
    (code: string | undefined) => {
      if (code === "station_unknown") {
        void queryClient.invalidateQueries({ queryKey: stationQueryKey });
        return;
      }
      if (!active) return;
      notifications.show({ color: "yellow", title: t("stations.operatorSignedOut"), message: "" });
      forget(active);
    },
    [active, forget, queryClient, t],
  );
  const install = useCallback(
    () => setStationOperator(() => (active ? held[active]?.token : undefined), signedOut),
    [active, held, signedOut],
  );
  useMemo(() => install(), [install]);
  useEffect(install, [install]);
  useEffect(() => () => setStationOperator(undefined), []);
  // What one operator read is not another's: their own work, their open log.
  const choose = (sessionId: string) => {
    queryClient.removeQueries({ queryKey: [MANUFACTURING_QUERY_KEY] });
    setActive(sessionId);
  };

  if (station.isPending)
    return (
      <Center h="100vh">
        <Loader />
      </Center>
    );
  const refusal = station.error ? refusalOf(station.error) : undefined;
  if (refusal?.code === "station_has_session") return <SignedInHere />;
  if (refusal?.status === 401) return <PairingForm />;
  if (station.error || !station.data)
    return (
      <Center h="100vh" p="md">
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("stations.couldNotLoad")}>
          {station.error?.message}
        </Alert>
      </Center>
    );
  return (
    <PairedStation
      station={station.data}
      readAt={station.dataUpdatedAt}
      held={held}
      active={active}
      onActive={choose}
      onSignedIn={(operator) => {
        setHeld((current) => ({ ...current, [operator.sessionId]: operator }));
        choose(operator.sessionId);
        void queryClient.invalidateQueries({ queryKey: stationQueryKey });
      }}
      onForget={forget}
    />
  );
};

/** A person is signed in in this browser: the station refuses until they sign out (M134, M138). */
const SignedInHere = () => {
  const { t } = useI18n("host");
  const out = useMutation({ mutationFn: signOut, onSettled: () => window.location.reload() });
  return (
    <Center h="100vh" p="md">
      <Card withBorder maw={520} p="xl">
        <Stack>
          <Title order={3}>{t("stations.signedInHereTitle")}</Title>
          <Text>{t("stations.signedInHereBody")}</Text>
          <Button size="lg" leftSection={<IconLogout size={20} />} loading={out.isPending} onClick={() => out.mutate()}>
            {t("stations.signOutHere")}
          </Button>
        </Stack>
      </Card>
    </Center>
  );
};

/** The pairing form (M129): the one-time code an administrator issued. */
const PairingForm = () => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const [code, setCode] = useState("");
  const pair = useMutation({
    mutationFn: () => pairStation(code),
    onSuccess: (data) => queryClient.setQueryData(stationQueryKey, data),
  });
  return (
    <Center h="100vh" p="md">
      <Card withBorder maw={520} w="100%" p="xl">
        <form
          onSubmit={(event) => {
            event.preventDefault();
            pair.mutate();
          }}
        >
          <Stack>
            <Title order={2}>{t("stations.pairTitle")}</Title>
            <Text c="dimmed">{t("stations.pairBody")}</Text>
            {pair.error && (
              <Alert color="red" icon={<IconAlertCircle size={16} />}>
                {refusalOf(pair.error).status === 429 ? t("stations.pairTooMany") : t("stations.pairInvalid")}
              </Alert>
            )}
            <TextInput
              size="xl"
              label={t("stations.pairingCode")}
              value={code}
              onChange={(event) => setCode(event.currentTarget.value)}
              autoComplete="off"
              autoFocus
            />
            <Button size="xl" type="submit" loading={pair.isPending} disabled={code.trim().length < 8}>
              {t("stations.pair")}
            </Button>
          </Stack>
        </form>
      </Card>
    </Center>
  );
};

/** The paired station: its bar, its operators and the shop floor for the active one. */
const PairedStation = ({
  station,
  readAt,
  held,
  active,
  onActive,
  onSignedIn,
  onForget,
}: {
  station: StationSelf;
  readAt: number;
  held: Record<string, HeldOperator>;
  active: string | null;
  onActive: (sessionId: string) => void;
  onSignedIn: (operator: HeldOperator) => void;
  onForget: (sessionId: string) => void;
}) => {
  const { t } = useI18n("host");
  const [switching, setSwitching] = useState(false);
  const [ending, setEnding] = useState<string | null>(null);
  const out = async (session: StationOperatorSession) => {
    setEnding(session.sessionId);
    try {
      await switchOut(session.sessionId);
    } catch {
      // Already ended: the chip goes either way.
    } finally {
      setEnding(null);
      onForget(session.sessionId);
    }
  };
  // A token the server no longer lists, in a read made after it was held, is
  // gone: an idle timeout, a replacement, a switch out elsewhere.
  useEffect(() => {
    const listed = new Set(station.operators.map((operator) => operator.sessionId));
    for (const operator of Object.values(held))
      if (operator.heldAt < readAt && !listed.has(operator.sessionId)) onForget(operator.sessionId);
  }, [station.operators, readAt, held, onForget]);
  const activeOperator = active ? held[active] : undefined;
  return (
    <LiveProvider userId={station.id}>
      <Stack p="md" gap="lg" mih="100vh">
        <Card withBorder padding="md">
          <Group justify="space-between" wrap="wrap" gap="md">
            <Title order={3}>{station.name}</Title>
            <Group gap="sm" wrap="wrap">
              {station.operators.map((operator) => {
                const mine = held[operator.sessionId];
                const chosen = active === operator.sessionId;
                return (
                  <Group key={operator.sessionId} gap={4} wrap="nowrap">
                    <UnstyledButton
                      onClick={() => mine && onActive(operator.sessionId)}
                      disabled={!mine}
                      aria-pressed={chosen}
                      aria-label={operator.displayName}
                    >
                      <Badge
                        size="xl"
                        variant={chosen ? "filled" : "light"}
                        color={mine ? "blue" : "gray"}
                        leftSection={<Avatar size="sm" name={operator.displayName} color="initials" />}
                      >
                        {operator.displayName}
                      </Badge>
                    </UnstyledButton>
                    <Button
                      size="compact-md"
                      variant="subtle"
                      color="red"
                      loading={ending === operator.sessionId}
                      onClick={() => void out(operator)}
                    >
                      {t("stations.switchOut")}
                    </Button>
                  </Group>
                );
              })}
              <Button size="lg" leftSection={<IconUserPlus size={20} />} onClick={() => setSwitching(true)}>
                {t("stations.switchIn")}
              </Button>
            </Group>
          </Group>
        </Card>
        {activeOperator ? (
          <>
            <IdleWarning
              key={activeOperator.sessionId}
              idleMinutes={station.idleMinutes}
              name={activeOperator.displayName}
              onExpired={() => onForget(activeOperator.sessionId)}
            />
            <StationShopFloor key={activeOperator.sessionId} />
          </>
        ) : (
          <Alert color="blue" title={t("stations.chooseOperatorTitle")}>
            {Object.keys(held).length > 0 ? t("stations.chooseOperatorBody") : t("stations.switchInFirst")}
          </Alert>
        )}
      </Stack>
      <SwitchInModal
        opened={switching}
        requirePin={station.requirePinWithBadge}
        onClose={() => setSwitching(false)}
        onSignedIn={(operator) => {
          setSwitching(false);
          onSignedIn(operator);
        }}
      />
    </LiveProvider>
  );
};

/**
 * The idle warning (M140): 30 seconds before the station's idle timeout the
 * operator is asked to stay; a tap anywhere counts as activity and *Stay
 * signed in* reads the floor again, which the server counts as activity too.
 */
const IdleWarning = ({
  idleMinutes,
  name,
  onExpired,
}: {
  idleMinutes: number;
  name: string;
  onExpired: () => void;
}) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const [last, setLast] = useState(() => Date.now());
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const touch = () => setLast(Date.now());
    document.addEventListener("pointerdown", touch);
    document.addEventListener("keydown", touch);
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => {
      document.removeEventListener("pointerdown", touch);
      document.removeEventListener("keydown", touch);
      window.clearInterval(timer);
    };
  }, []);
  const left = idleMinutes * 60_000 - (now - last);
  useEffect(() => {
    if (left <= 0) onExpired();
  }, [left, onExpired]);
  if (left > IDLE_WARNING_MS) return null;
  return (
    <Alert color="orange" icon={<IconAlertTriangle size={20} />} title={t("stations.idleTitle", { name })}>
      <Group justify="space-between">
        <Text>{t("stations.idleBody", { seconds: Math.max(0, Math.ceil(left / 1000)) })}</Text>
        <Button size="lg" onClick={() => void queryClient.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY] })}>
          {t("stations.staySignedIn")}
        </Button>
      </Group>
    </Alert>
  );
};

/**
 * Switch in (M133): the people who may, by name, then a PIN pad; or a badge,
 * whose scan field always has the focus — a keyboard-wedge reader types the
 * code and Enter. A badge that needs the PIN too asks for it after the scan.
 */
const SwitchInModal = ({
  opened,
  requirePin,
  onClose,
  onSignedIn,
}: {
  opened: boolean;
  requirePin: boolean;
  onClose: () => void;
  onSignedIn: (operator: HeldOperator) => void;
}) => {
  const { t } = useI18n("host");
  const candidates = useQuery({ queryKey: ["station", "operators"], queryFn: listStationOperators, enabled: opened });
  const [person, setPerson] = useState<StationOperatorCandidate | null>(null);
  const [badge, setBadge] = useState<string | null>(null);
  const [scan, setScan] = useState("");
  const [pin, setPin] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const badgeField = useRef<HTMLInputElement>(null);
  const reset = () => {
    setPerson(null);
    setBadge(null);
    setScan("");
    setPin("");
    setFailure(null);
  };
  const signIn = useMutation({
    mutationFn: (body: { userId?: string; pin?: string; badge?: string }) => switchIn(body),
    onSuccess: (answer) => {
      reset();
      onSignedIn({
        sessionId: answer.sessionId,
        token: answer.token,
        displayName: answer.operator.displayName,
        userId: answer.operator.userId,
        heldAt: Date.now(),
      });
    },
    onError: (error) => {
      const { status, code } = refusalOf(error);
      setPin("");
      setFailure(
        status === 429
          ? t("stations.signInTooMany")
          : code === "too_many_operators"
            ? t("stations.tooManyOperators")
            : t("stations.signInFailed"),
      );
    },
  });
  useEffect(() => {
    if (opened && !person && !badge) badgeField.current?.focus();
  });
  const enterPin = (value: string) => {
    setPin(value);
    if (value.length !== 6) return;
    if (badge) signIn.mutate({ badge, pin: value });
    else if (person) signIn.mutate({ userId: person.userId, pin: value });
  };
  return (
    <Modal
      opened={opened}
      onClose={() => {
        reset();
        onClose();
      }}
      title={t("stations.switchIn")}
      size="xl"
      centered
    >
      <Stack>
        {failure && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {failure}
          </Alert>
        )}
        {person || badge ? (
          <Stack align="center">
            <Text size="lg">
              {person ? t("stations.pinFor", { name: person.displayName }) : t("stations.pinForBadge")}
            </Text>
            <PinInput
              size="xl"
              length={6}
              type="number"
              mask
              oneTimeCode={false}
              value={pin}
              onChange={enterPin}
              disabled={signIn.isPending}
              aria-label={t("stations.pin")}
              autoFocus
            />
            <Button variant="default" size="lg" onClick={reset}>
              {t("stations.back")}
            </Button>
          </Stack>
        ) : (
          <>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                const code = scan.trim();
                setScan("");
                if (!code) return;
                if (requirePin) setBadge(code);
                else signIn.mutate({ badge: code });
              }}
            >
              <TextInput
                ref={badgeField}
                label={t("stations.scanBadge")}
                description={t("stations.scanBadgeHint")}
                value={scan}
                onChange={(event) => setScan(event.currentTarget.value)}
                autoComplete="off"
                type="password"
              />
            </form>
            <Text fw={600}>{t("stations.choosePerson")}</Text>
            {candidates.isPending ? (
              <Loader />
            ) : (candidates.data ?? []).length === 0 ? (
              <Text c="dimmed">{t("stations.nobodyMay")}</Text>
            ) : (
              <SimpleGrid cols={{ base: 2, sm: 3, md: 4 }}>
                {(candidates.data ?? [])
                  .filter((candidate) => candidate.hasPin)
                  .map((candidate) => (
                    <Card
                      key={candidate.userId}
                      withBorder
                      padding="md"
                      renderRoot={(props) => (
                        <button
                          type="button"
                          {...props}
                          onClick={() => setPerson(candidate)}
                          style={{ ...props.style, width: "100%" }}
                        />
                      )}
                    >
                      <Stack align="center" gap="xs">
                        <Avatar size="lg" name={candidate.displayName} color="initials" />
                        <Text ta="center">{candidate.displayName}</Text>
                      </Stack>
                    </Card>
                  ))}
              </SimpleGrid>
            )}
          </>
        )}
      </Stack>
    </Modal>
  );
};

export const Route = createFileRoute("/station")({ component: StationPage });
