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
import { LiveProvider, setTimeZones, useI18n } from "@vantigo/frontend-shell";
import {
  MANUFACTURING_QUERY_KEY,
  manufacturingMetaQueryOptions,
  StationShopFloor,
  setStationOperator,
} from "@vantigo/manufacturing-ui";
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

/**
 * How often the station reads itself again: an operator's session ended
 * elsewhere — by an administrator, a replacement — shows here within this,
 * without a tap. The read is the station's own, with its cookie alone, so it
 * keeps no operator's session alive.
 */
const STATION_POLL_MS = 5_000;

/** The station as read, and when the read was sent: a sign-in after that is not in it. */
type StationRead = { self: StationSelf; startedAt: number };

const readStationNow = async (): Promise<StationRead> => {
  const startedAt = Date.now();
  return { self: await readStation(), startedAt };
};

/**
 * An operator signed in at this station, as this page holds them: in memory
 * only (M133). `lastRequestAt` is when a request made with their token was
 * last answered (or the server's last activity, when that is later): the
 * server's idle window has slid to within `every` of it. A signed-out
 * operator keeps a chip that says so until it is dismissed.
 */
type HeldOperator = {
  sessionId: string;
  token: string;
  displayName: string;
  userId: string;
  heldAt: number;
  lastRequestAt: number;
  signedOut?: boolean;
};

/** The held operators but for the person's signed-out chips: switching in again replaces them. */
const withoutSignedOut = (held: Record<string, HeldOperator>, userId: string) =>
  Object.fromEntries(
    Object.entries(held).filter(([, operator]) => !(operator.signedOut && operator.userId === userId)),
  );

/** How long before an idle session ends the warning shows (M140). */
const IDLE_WARNING_MS = 30_000;

/**
 * How stale the server's record of an operator's activity may be: it slides
 * its idle window only once the window is a quarter of the timeout old, at
 * most a minute (identity's checkStation).
 */
const slideEvery = (idleMinutes: number) => Math.min((idleMinutes * 60_000) / 4, 60_000);

/** When the server ends an operator's session if nothing more is asked with their token. */
const expiresAt = (operator: HeldOperator, idleMinutes: number) =>
  operator.lastRequestAt - slideEvery(idleMinutes) + idleMinutes * 60_000;

/**
 * The shared shop-floor station (manufacturing phase 2 M140): outside the app
 * layout, as sign-in is. Unpaired, the pairing form; paired, a bar with the
 * station's name and its operators' chips, Switch in with the people who may
 * and a PIN pad or a badge scan, and below it the shop floor for the operator
 * whose chip is chosen — every request with that operator's token, which this
 * page holds in memory only: a reload asks the operators to switch in again.
 * Dates and times show in the workspace's zone, which the station reads.
 */
const StationPage = () => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const station = useQuery({
    queryKey: stationQueryKey,
    queryFn: readStationNow,
    retry: false,
    refetchInterval: (query) => (query.state.error ? false : STATION_POLL_MS),
  });
  const [held, setHeld] = useState<Record<string, HeldOperator>>({});
  const [active, setActive] = useState<string | null>(null);
  const zone = station.data?.self.timeZone;
  useEffect(() => {
    if (zone) setTimeZones({ timeZone: zone, businessTimeZone: zone });
  }, [zone]);

  /**
   * The operator is signed out: their token is dropped, their chip says so —
   * the one chip that does for that person, an older signed-out one put away.
   */
  const forget = useCallback(
    (sessionId: string) => {
      setHeld((current) => {
        const operator = current[sessionId];
        if (!operator) return current;
        return {
          ...withoutSignedOut(current, operator.userId),
          [sessionId]: { ...operator, signedOut: true, token: "" },
        };
      });
      setActive((current) => (current === sessionId ? null : current));
      void queryClient.invalidateQueries({ queryKey: stationQueryKey });
    },
    [queryClient],
  );
  /** A chip put away: switched out, or a signed-out one dismissed. */
  const dismiss = useCallback(
    (sessionId: string) => {
      setHeld((current) => Object.fromEntries(Object.entries(current).filter(([key]) => key !== sessionId)));
      setActive((current) => (current === sessionId ? null : current));
      void queryClient.invalidateQueries({ queryKey: stationQueryKey });
    },
    [queryClient],
  );

  // The manufacturing pages' requests go out as the active operator, each
  // tagged with the token it carried. A 401 is the station's: the operator
  // whose token the request carried is signed out — whoever is active now —
  // or the station itself is unpaired. Every other answer tells the idle
  // clock that operator was active. Installed while rendering, so before the
  // shop floor's first request, again after a remount, and cleared when the
  // page goes.
  const signedOut = useCallback(
    (code: string | undefined, token: string | undefined) => {
      if (code === "station_unknown") {
        void queryClient.invalidateQueries({ queryKey: stationQueryKey });
        return;
      }
      const operator = token ? Object.values(held).find((one) => one.token === token) : undefined;
      if (!operator || operator.signedOut) return;
      notifications.show({ color: "yellow", title: t("stations.operatorSignedOut"), message: operator.displayName });
      forget(operator.sessionId);
    },
    [held, forget, queryClient, t],
  );
  const answered = useCallback((token: string) => {
    const now = Date.now();
    setHeld((current) => {
      const operator = Object.values(current).find((one) => one.token === token);
      return operator
        ? { ...current, [operator.sessionId]: { ...current[operator.sessionId], lastRequestAt: now } }
        : current;
    });
  }, []);
  const install = useCallback(
    () => setStationOperator(() => (active ? held[active]?.token || undefined : undefined), signedOut, answered),
    [active, held, signedOut, answered],
  );
  useMemo(() => install(), [install]);
  useEffect(install, [install]);
  useEffect(() => () => setStationOperator(undefined), []);
  // What one operator read is not another's: their own work, their open log.
  // The idle clock starts from what the server last saw of them, if later.
  const choose = (sessionId: string) => {
    queryClient.removeQueries({ queryKey: [MANUFACTURING_QUERY_KEY] });
    const listed = station.data?.self.operators.find((operator) => operator.sessionId === sessionId);
    const serverSaw = listed ? Date.parse(listed.lastActivityAt) : Number.NaN;
    setHeld((current) =>
      current[sessionId] && Number.isFinite(serverSaw) && serverSaw > current[sessionId].lastRequestAt
        ? { ...current, [sessionId]: { ...current[sessionId], lastRequestAt: serverSaw } }
        : current,
    );
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
      station={station.data.self}
      readStartedAt={station.data.startedAt}
      held={held}
      active={active}
      onActive={choose}
      onSignedIn={(operator) => {
        setHeld((current) => ({ ...withoutSignedOut(current, operator.userId), [operator.sessionId]: operator }));
        queryClient.removeQueries({ queryKey: [MANUFACTURING_QUERY_KEY] });
        setActive(operator.sessionId);
        void queryClient.invalidateQueries({ queryKey: stationQueryKey });
      }}
      onForget={forget}
      onDismiss={dismiss}
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
    onSuccess: (data) => queryClient.setQueryData<StationRead>(stationQueryKey, { self: data, startedAt: Date.now() }),
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
  readStartedAt,
  held,
  active,
  onActive,
  onSignedIn,
  onForget,
  onDismiss,
}: {
  station: StationSelf;
  readStartedAt: number;
  held: Record<string, HeldOperator>;
  active: string | null;
  onActive: (sessionId: string) => void;
  onSignedIn: (operator: HeldOperator) => void;
  onForget: (sessionId: string) => void;
  onDismiss: (sessionId: string) => void;
}) => {
  const { t } = useI18n("host");
  const [switching, setSwitching] = useState(false);
  const [ending, setEnding] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const out = async (sessionId: string) => {
    setEnding(sessionId);
    try {
      await switchOut(sessionId);
    } catch {
      // Already ended: the chip goes either way.
    } finally {
      setEnding(null);
      onDismiss(sessionId);
    }
  };
  // A token the server no longer lists, in a read sent after it was held, is
  // gone: an idle timeout, a replacement, a switch out elsewhere. A read sent
  // before the sign-in cannot list it, whenever its answer arrives.
  useEffect(() => {
    const listed = new Set(station.operators.map((operator) => operator.sessionId));
    for (const operator of Object.values(held))
      if (!operator.signedOut && operator.heldAt < readStartedAt && !listed.has(operator.sessionId))
        onForget(operator.sessionId);
  }, [station.operators, readStartedAt, held, onForget]);
  // Every held operator's idle clock runs, chosen or not: one whose session
  // the server has ended for idleness is marked signed out here too.
  useEffect(() => {
    for (const operator of Object.values(held))
      if (!operator.signedOut && now >= expiresAt(operator, station.idleMinutes)) onForget(operator.sessionId);
  }, [now, held, station.idleMinutes, onForget]);
  const activeOperator = active ? held[active] : undefined;
  const chips: (StationOperatorSession | HeldOperator)[] = [
    ...station.operators,
    ...Object.values(held).filter(
      (operator) => operator.signedOut && !station.operators.some((one) => one.sessionId === operator.sessionId),
    ),
  ];
  return (
    <LiveProvider userId={station.id}>
      <Stack p="md" gap="lg" mih="100vh">
        <Card withBorder padding="md">
          <Group justify="space-between" wrap="wrap" gap="md">
            <Title order={3}>{station.name}</Title>
            <Group gap="sm" wrap="wrap">
              {chips.map((operator) => {
                const mine = held[operator.sessionId];
                const gone = mine?.signedOut === true;
                const chosen = active === operator.sessionId;
                return (
                  <Group key={operator.sessionId} gap={4} wrap="nowrap">
                    <UnstyledButton
                      onClick={() => mine && !gone && onActive(operator.sessionId)}
                      disabled={!mine || gone}
                      aria-pressed={chosen}
                      aria-label={operator.displayName}
                    >
                      <Badge
                        size="xl"
                        variant={chosen ? "filled" : "light"}
                        color={gone ? "orange" : mine ? "blue" : "gray"}
                        leftSection={<Avatar size="sm" name={operator.displayName} color="initials" />}
                      >
                        {gone ? `${operator.displayName} · ${t("stations.operatorSignedOut")}` : operator.displayName}
                      </Badge>
                    </UnstyledButton>
                    <Button
                      size="compact-md"
                      variant="subtle"
                      color={gone ? "gray" : "red"}
                      loading={ending === operator.sessionId}
                      onClick={() => (gone ? onDismiss(operator.sessionId) : void out(operator.sessionId))}
                    >
                      {gone ? t("stations.dismiss") : t("stations.switchOut")}
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
        {activeOperator && !activeOperator.signedOut ? (
          <>
            <IdleWarning
              key={activeOperator.sessionId}
              idleMinutes={station.idleMinutes}
              name={activeOperator.displayName}
              lastRequestAt={activeOperator.lastRequestAt}
              now={now}
            />
            <StationShopFloor key={activeOperator.sessionId} />
          </>
        ) : (
          <Alert color="blue" title={t("stations.chooseOperatorTitle")}>
            {Object.values(held).some((operator) => !operator.signedOut)
              ? t("stations.chooseOperatorBody")
              : t("stations.switchInFirst")}
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
 * The idle warning (M140), for the active operator: their session ends when
 * the server has seen no request with their token for the station's timeout,
 * so the clock runs from the last request answered (`lastRequestAt`), less
 * the minute the server may lag in recording it. Live updates refetch with
 * the active token and keep the session alive. A tap or a key, once a request
 * would slide the server's window, reads Manufacturing's meta — the cheapest
 * request open to stations — so working the screen counts; *Stay signed in*
 * does the same at once and reads the floor again.
 */
const IdleWarning = ({
  idleMinutes,
  name,
  lastRequestAt,
  now,
}: {
  idleMinutes: number;
  name: string;
  lastRequestAt: number;
  now: number;
}) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const every = slideEvery(idleMinutes);
  const touch = useCallback(
    () => queryClient.fetchQuery({ ...manufacturingMetaQueryOptions(), staleTime: 0 }).catch(() => undefined),
    [queryClient],
  );
  const lastRef = useRef(lastRequestAt);
  useEffect(() => {
    lastRef.current = lastRequestAt;
  }, [lastRequestAt]);
  useEffect(() => {
    const activity = () => {
      if (Date.now() - lastRef.current >= every) void touch();
    };
    document.addEventListener("pointerdown", activity);
    document.addEventListener("keydown", activity);
    return () => {
      document.removeEventListener("pointerdown", activity);
      document.removeEventListener("keydown", activity);
    };
  }, [every, touch]);
  const left = lastRequestAt - every + idleMinutes * 60_000 - now;
  if (left > IDLE_WARNING_MS || left <= 0) return null;
  return (
    <Alert color="orange" icon={<IconAlertTriangle size={20} />} title={t("stations.idleTitle", { name })}>
      <Group justify="space-between">
        <Text>{t("stations.idleBody", { seconds: Math.max(0, Math.ceil(left / 1000)) })}</Text>
        <Button
          size="lg"
          onClick={() => {
            void touch();
            void queryClient.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY] });
          }}
        >
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
        lastRequestAt: Date.now(),
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
