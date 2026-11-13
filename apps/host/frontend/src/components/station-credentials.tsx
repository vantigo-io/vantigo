import { Badge, Button, Group, PasswordInput, PinInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDeviceTablet } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  accountPinQueryKey,
  clearPin,
  enrolBadge,
  operatorCredentialsQueryKey,
  readAccountPin,
  readOperatorCredentials,
  removeAccountPin,
  removeBadge,
  setAccountPin,
} from "../api/stations";

/** A PIN refusal in the person's words (manufacturing phase 2 M132). */
const pinRefusal = (error: unknown, t: (key: string) => string) => {
  const code = (error as { code?: string } | null)?.code;
  if (code === "pin_too_simple") return t("stations.pinTooSimple");
  if (code === "reauthentication_required") return t("stations.pinReauth");
  if (code === "invalid_credentials") return t("stations.pinWrongPassword");
  if (code === "invalid_request") return t("stations.pinInvalid");
  return (error as Error).message;
};

/**
 * Settings › Security › Station PIN (manufacturing phase 2 M132): the six
 * digits a person switches in with at a shared shop-floor station. Setting it
 * asks for the current password unless the person signed in within ten
 * minutes; it is never a way to sign in to Vantigo.
 */
export const StationPinSection = () => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const state = useQuery({ queryKey: accountPinQueryKey, queryFn: readAccountPin });
  const [pin, setPin] = useState("");
  const [password, setPassword] = useState("");
  const done = (title: string) => async () => {
    setPin("");
    setPassword("");
    notifications.show({ color: "teal", title, message: "" });
    await queryClient.invalidateQueries({ queryKey: accountPinQueryKey });
  };
  const save = useMutation({
    mutationFn: () => setAccountPin(pin, password || undefined),
    onSuccess: done(t("stations.pinSaved")),
    onError: (error) =>
      notifications.show({ color: "red", title: t("stations.pinSection"), message: pinRefusal(error, t) }),
  });
  const remove = useMutation({ mutationFn: removeAccountPin, onSuccess: done(t("stations.pinRemoved")) });
  return (
    <PageSection>
      <Stack>
        <Group justify="space-between">
          <div>
            <Title order={4}>{t("stations.pinSection")}</Title>
            <Text size="sm" c="dimmed">
              {t("stations.pinSectionHint")}
            </Text>
          </div>
          <IconDeviceTablet size={22} />
        </Group>
        <Text size="sm">
          {state.data?.set && state.data.setAt
            ? t("stations.pinIsSet", { when: formatters.formatDate(state.data.setAt, { dateStyle: "medium" }) })
            : t("stations.pinNotSet")}
        </Text>
        {state.data?.needsReenrolment && (
          <Text size="sm" c="orange">
            {t("stations.pinNeedsReenrolment")}
          </Text>
        )}
        <form
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <Stack>
            <Stack gap={4}>
              <Text size="sm" fw={500}>
                {t("stations.newPin")}
              </Text>
              <PinInput length={6} type="number" mask value={pin} onChange={setPin} aria-label={t("stations.newPin")} />
            </Stack>
            <PasswordInput
              label={t("stations.currentPassword")}
              description={t("stations.currentPasswordHint")}
              value={password}
              onChange={(event) => setPassword(event.currentTarget.value)}
            />
            <Group>
              <Button type="submit" loading={save.isPending} disabled={pin.length !== 6}>
                {t("stations.setPin")}
              </Button>
              {state.data?.set && (
                <Button variant="default" color="red" loading={remove.isPending} onClick={() => remove.mutate()}>
                  {t("stations.removePin")}
                </Button>
              )}
            </Group>
          </Stack>
        </form>
      </Stack>
    </PageSection>
  );
};

/**
 * A person's station credentials on Workspace › Users (M132): whether they
 * have a PIN, which an administrator can only clear, and their badge, which
 * an administrator enrols by scanning the card into the field.
 */
export const OperatorCredentialsSection = ({ userId }: { userId: string }) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const key = operatorCredentialsQueryKey(userId);
  const state = useQuery({ queryKey: key, queryFn: () => readOperatorCredentials(userId) });
  const [badge, setBadge] = useState("");
  const [password, setPassword] = useState("");
  const after = (title: string) => async () => {
    setBadge("");
    setPassword("");
    notifications.show({ color: "teal", title, message: "" });
    await queryClient.invalidateQueries({ queryKey: key });
  };
  const fail = (error: unknown) => {
    const code = (error as { code?: string }).code;
    notifications.show({
      color: "red",
      title: t("stations.operatorSection"),
      message:
        code === "badge_unavailable"
          ? t("stations.badgeUnavailable")
          : code === "reauthentication_required"
            ? t("stations.reauth")
            : code === "invalid_credentials"
              ? t("stations.pinWrongPassword")
              : (error as Error).message,
    });
  };
  const clear = useMutation({
    mutationFn: () => clearPin(userId),
    onSuccess: after(t("stations.pinCleared")),
    onError: fail,
  });
  const enrol = useMutation({
    mutationFn: () => enrolBadge(userId, badge, password || undefined),
    onSuccess: after(t("stations.badgeSaved")),
    onError: fail,
  });
  const drop = useMutation({
    mutationFn: () => removeBadge(userId),
    onSuccess: after(t("stations.badgeRemoved")),
    onError: fail,
  });
  if (!state.data) return null;
  return (
    <Stack gap="xs">
      <Text fw={600} size="sm">
        {t("stations.operatorSection")}
      </Text>
      <Group gap="sm">
        <Badge variant="light" color={state.data.pinNeedsReenrolment ? "orange" : state.data.hasPin ? "teal" : "gray"}>
          {state.data.pinNeedsReenrolment
            ? t("stations.needsReenrolment")
            : state.data.hasPin
              ? t("stations.hasPin")
              : t("stations.noPin")}
        </Badge>
        {state.data.hasPin && (
          <Button
            size="compact-sm"
            variant="subtle"
            color="red"
            loading={clear.isPending}
            onClick={() => clear.mutate()}
          >
            {t("stations.clearPin")}
          </Button>
        )}
        <Badge
          variant="light"
          color={state.data.badgeNeedsReenrolment ? "orange" : state.data.hasBadge ? "teal" : "gray"}
        >
          {state.data.badgeNeedsReenrolment
            ? t("stations.needsReenrolment")
            : state.data.hasBadge
              ? t("stations.hasBadge")
              : t("stations.noBadge")}
        </Badge>
        {state.data.hasBadge && (
          <Button size="compact-sm" variant="subtle" color="red" loading={drop.isPending} onClick={() => drop.mutate()}>
            {t("stations.removeBadge")}
          </Button>
        )}
      </Group>
      <Group align="flex-end">
        <TextInput
          label={t("stations.badge")}
          description={t("stations.badgeHint")}
          type="password"
          autoComplete="off"
          value={badge}
          onChange={(event) => setBadge(event.currentTarget.value)}
          onKeyDown={(event) => {
            // A card reader ends its code with Enter: enrol rather than submit the user form.
            if (event.key === "Enter") {
              event.preventDefault();
              if (badge.trim()) enrol.mutate();
            }
          }}
        />
        <PasswordInput
          label={t("stations.currentPassword")}
          description={t("stations.currentPasswordHint")}
          value={password}
          onChange={(event) => setPassword(event.currentTarget.value)}
        />
        <Button variant="default" loading={enrol.isPending} disabled={!badge.trim()} onClick={() => enrol.mutate()}>
          {t("stations.enrolBadge")}
        </Button>
      </Group>
    </Stack>
  );
};
