import { Alert, Button, Card, Center, Code, Group, Loader, Radio, Select, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { AuthBrandMark, appUrl, useBrand, useI18n, vantigoTokens } from "@vantigo/frontend-shell";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import {
  decideAuthorization,
  type OAuthAuthorizationPreview,
  type OAuthIntegrationChoice,
  previewAuthorization,
} from "../api/oauth";
import { mfaEnrolmentDestination } from "../lib/mfa-enrolment-gate";
import { authorizeQuery, rememberOAuthReturn, restoreAuthorizeAddress } from "../lib/oauth-return";
import "../i18n";

type Access = "read" | "read_write";
type Translate = (key: string, options?: Record<string, unknown>) => string;

/** The page's ways out of itself, in one place the tests can watch. */
export const oauthPageNavigation = {
  assign: (to: string) => window.location.assign(to),
  closeWindow: () => window.close(),
};

const loopbackHosts = new Set(["127.0.0.1", "[::1]", "localhost"]);

/**
 * Whether the page may send the browser to an address: https, or http to this
 * computer, as a redirect URI may be (C9). The server answered the address,
 * and this is the page's own check that it is one: nothing else — no
 * javascript:, no data:, no plain http to another host — is followed.
 */
export const isAllowedRedirect = (to: string): boolean => {
  let url: URL;
  try {
    url = new URL(to);
  } catch {
    return false;
  }
  if (url.username || url.password) return false;
  return url.protocol === "https:" || (url.protocol === "http:" && loopbackHosts.has(url.hostname));
};

/** Sends the browser to an address the server answered with, when it is allowed; answers whether it went. */
const go = (to: string): boolean => {
  if (!isAllowedRedirect(to)) return false;
  oauthPageNavigation.assign(to);
  return true;
};

/** Leaves the page: the tab when the client opened it, else the workspace. */
const close = () => {
  oauthPageNavigation.closeWindow();
  oauthPageNavigation.assign(appUrl("/"));
};

/** Back to sign-in, with the way back kept, for a session that is missing or has expired. */
const toSignIn = (query: string) => {
  rememberOAuthReturn(query);
  oauthPageNavigation.assign(appUrl("/sign-in"));
};

const statusOf = (error: unknown) => (error as { status?: number } | null)?.status;
const codeOf = (error: unknown) => (error as { code?: string } | null)?.code;

/** A name the client chose, kept to its box however it is built. */
const clipped = { overflowWrap: "anywhere", overflow: "hidden" } as const;

/**
 * Why the server refused, in the reader's language: the page's own words for
 * the OAuth error code, and the server's English reason beside them on an
 * English page, where it is the only place an operator learns which rule a
 * client broke. An unknown code falls back to the server's reason.
 */
const Reason = ({ error, message }: { error?: string | null; message?: string | null }) => {
  const { t, locale } = useI18n("host");
  const key = `oauth.error_${error ?? ""}`;
  const known = !!error && t(key) !== key;
  const detail = !known || locale === "en" ? message : undefined;
  return (
    <>
      {known && <Text size="sm">{t(key)}</Text>}
      {detail && (
        <Text size="sm" c="dimmed" style={clipped}>
          {detail}
        </Text>
      )}
    </>
  );
};

/** The centred card every state of the page is shown in, as the sign-in pages are. */
const Frame = ({ children }: { children: ReactNode }) => (
  <Center mih="100vh" bg={vantigoTokens.pageBg}>
    <Card withBorder p="xl" maw={520} w="100%">
      <Stack>
        <AuthBrandMark />
        {children}
      </Stack>
    </Card>
  </Center>
);

const Waiting = () => {
  const { t } = useI18n("host");
  return (
    <Center mih="100vh" bg={vantigoTokens.pageBg}>
      <Loader size="sm" role="status" aria-label={t("oauth.loading")} />
    </Center>
  );
};

/** A state that offers nothing but Close: a refused client, a person who may not approve, a failure. */
const Refused = ({ title, children }: { title: string; children: ReactNode }) => {
  const { t } = useI18n("host");
  return (
    <Frame>
      <Title order={2}>{title}</Title>
      {children}
      <Group justify="flex-end">
        <Button variant="default" onClick={close}>
          {t("oauth.close")}
        </Button>
      </Group>
    </Frame>
  );
};

const Blocked = () => {
  const { t } = useI18n("host");
  return (
    <Refused title={t("oauth.failedTitle")}>
      <Alert color="red">{t("oauth.blocked")}</Alert>
    </Refused>
  );
};

/**
 * The OAuth consent page (OAuth connections design C13, C14, A6–A8, A26, A27).
 * A client sends the browser here with an authorization request. The page
 * reads the session itself: without one, or once it has expired, it stores its
 * return and goes to sign in; a person who is not an Owner is told to ask one,
 * and nothing is fetched; an Owner held at MFA enrolment goes to enrol, and the
 * security page offers the way back. An Owner's request is judged by the
 * server first, client and redirect URI before anything else, and the page
 * follows its answer exactly: a refused client is never redirected to; an
 * error goes back to the client at once only when the server says so, else
 * behind a "Return to <host>" button; otherwise the client is shown as it
 * calls itself beside the hosts that were verified, and the Owner picks the
 * integration and the access, Read only unless they choose more. Every address
 * the page sends the browser to is the server's answer and passes
 * isAllowedRedirect. The page is never framed: the server's headers forbid it,
 * and the page renders nothing inside a frame.
 */
export const OAuthAuthorizePage = ({ query }: { query: string }) => {
  const { t } = useI18n("host");
  const framed = typeof window !== "undefined" && window.top !== window.self;
  const session = useQuery({
    queryKey: sessionQueryKey,
    queryFn: fetchSession,
    staleTime: 0,
    retry: false,
    enabled: !framed,
  });
  const isOwner = session.data?.user.roles.includes("Owner") === true;
  const mustEnrol = isOwner && session.data?.mfaEnrollmentRequired === true;
  const preview = useQuery({
    queryKey: previewQueryKey(query),
    queryFn: () => previewAuthorization(query),
    enabled: !framed && isOwner && !mustEnrol && query !== "",
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const expired = preview.isError && statusOf(preview.error) === 401;

  useEffect(() => {
    if (framed || session.isPending || session.isError) return;
    if (!session.data || expired) toSignIn(query);
    else if (mustEnrol) {
      // Stamped now, so the ten minutes run from here and not from an earlier visit.
      rememberOAuthReturn(query);
      oauthPageNavigation.assign(appUrl(mfaEnrolmentDestination));
    }
  }, [framed, session.isPending, session.isError, session.data, mustEnrol, expired, query]);

  const answer = preview.data;
  const automatic = answer?.outcome === "redirect" && answer.automatic === true && !!answer.redirectTo;
  const automaticRefused = automatic && !isAllowedRedirect(answer?.redirectTo ?? "");
  useEffect(() => {
    if (automatic && !automaticRefused && answer?.redirectTo) go(answer.redirectTo);
  }, [automatic, automaticRefused, answer?.redirectTo]);

  if (framed) return null;
  if (automaticRefused) return <Blocked />;
  if (session.isError)
    return (
      <Refused title={t("oauth.failedTitle")}>
        <Alert color="red">{t("oauth.failed")}</Alert>
      </Refused>
    );
  if (session.isPending || !session.data) return <Waiting />;
  if (!isOwner)
    return (
      <Refused title={t("oauth.askOwnerTitle")}>
        <Text>{t("oauth.askOwner")}</Text>
      </Refused>
    );
  if (mustEnrol || expired) return <Waiting />;
  if (query === "")
    return (
      <Refused title={t("oauth.refusedTitle")}>
        <Alert color="red">{t("oauth.noRequest")}</Alert>
      </Refused>
    );
  if (preview.isPending || automatic) return <Waiting />;
  if (preview.isError) return <Failed error={preview.error} t={t} />;
  if (answer?.outcome === "redirect" && answer.redirectTo)
    return <ReturnWithError answer={answer} redirectTo={answer.redirectTo} />;
  if (answer?.outcome === "consent" && answer.client) return <Consent query={query} answer={answer} />;
  return (
    <Refused title={t("oauth.refusedTitle")}>
      <Text>{t("oauth.refused")}</Text>
      <Reason error={answer?.error} message={answer?.message} />
    </Refused>
  );
};

const previewQueryKey = (query: string) => ["oauth", "authorize", "preview", query] as const;

/** Why a preview or an approval failed, as the person can act on it. */
const failureText = (error: unknown, t: Translate) => {
  const status = statusOf(error);
  if (codeOf(error) === "integration_not_eligible") return t("oauth.notEligible");
  if (status === 404) return t("oauth.switchedOff");
  if (status === 429) return t("oauth.tooMany");
  if (status === 403) return t("oauth.forbidden");
  return t("oauth.failed");
};

const Failed = ({ error, t }: { error: unknown; t: Translate }) => (
  <Refused title={t("oauth.failedTitle")}>
    <Alert color="red">{failureText(error, t)}</Alert>
  </Refused>
);

/** An error the client is told about, behind a click: the server did not judge its host safe to send to unasked (A26). */
const ReturnWithError = ({ answer, redirectTo }: { answer: OAuthAuthorizationPreview; redirectTo: string }) => {
  const { t } = useI18n("host");
  const [blocked, setBlocked] = useState(false);
  if (blocked) return <Blocked />;
  const host = answer.returnHost ?? "";
  return (
    <Frame>
      <Title order={2}>{t("oauth.requestErrorTitle")}</Title>
      <Text>{t("oauth.requestError")}</Text>
      <Reason error={answer.error} message={answer.message} />
      <Group justify="flex-end">
        <Button variant="default" onClick={close}>
          {t("oauth.close")}
        </Button>
        <Button onClick={() => go(redirectTo) || setBlocked(true)}>{t("oauth.returnTo", { host })}</Button>
      </Group>
    </Frame>
  );
};

const choiceLabel = (choice: OAuthIntegrationChoice, t: Translate) =>
  choice.eligible || !choice.reason ? choice.name : `${choice.name} — ${t(`oauth.notEligible_${choice.reason}`)}`;

const Consent = ({ query, answer }: { query: string; answer: OAuthAuthorizationPreview }) => {
  const { t } = useI18n("host");
  const brand = useBrand();
  const queryClient = useQueryClient();
  const client = answer.client as NonNullable<OAuthAuthorizationPreview["client"]>;
  const choices = answer.integrations ?? [];
  // No integration is preselected and access starts at Read only (A7).
  const [integrationId, setIntegrationId] = useState<string | null>(null);
  const [access, setAccess] = useState<Access>("read");
  const [blocked, setBlocked] = useState(false);
  // One decision per page: a second press while the first is on its way posts nothing.
  const sent = useRef(false);
  const chosen = choices.find((choice) => choice.id === integrationId && choice.eligible);
  const writeAllowed = chosen?.maxAccess === "read_write";
  const decide = useMutation({
    mutationFn: (decision: "approve" | "deny") =>
      decideAuthorization(
        decision === "approve" && chosen
          ? { query, decision, integrationId: chosen.id, access: writeAllowed ? access : "read" }
          : { query, decision },
      ),
    onSuccess: (result) => {
      if (!go(result.redirectTo)) setBlocked(true);
    },
    onError: (error) => {
      sent.current = false;
      if (statusOf(error) === 401) toSignIn(query);
      // The integrations changed since the preview: read them again.
      else if (codeOf(error) === "integration_not_eligible")
        void queryClient.invalidateQueries({ queryKey: previewQueryKey(query) });
    },
  });
  const send = (decision: "approve" | "deny") => {
    if (sent.current) return;
    sent.current = true;
    decide.mutate(decision);
  };
  if (blocked) return <Blocked />;
  if (decide.isError && codeOf(decide.error) === "client_refused")
    return (
      <Refused title={t("oauth.refusedTitle")}>
        <Text>{t("oauth.refused")}</Text>
      </Refused>
    );
  if (decide.isError && statusOf(decide.error) === 401) return <Waiting />;
  const busy = decide.isPending || decide.isSuccess;

  return (
    <Frame>
      <Title order={2}>{t("oauth.title", { app: brand.title })}</Title>
      <Stack gap={4}>
        <Text size="sm" c="dimmed">
          {t("oauth.callsItself")}
        </Text>
        <Text fw={700} size="lg" lineClamp={2} style={clipped} data-testid="oauth-client-name">
          {client.name}
        </Text>
      </Stack>
      <Stack gap={4} data-testid="oauth-verified">
        <Text size="sm">
          {client.loopback ? t("oauth.documentAtLoopback") : t("oauth.documentAt", { app: brand.title })}{" "}
          <Code>{client.clientIdHost}</Code>
        </Text>
        <Text size="sm">
          {t("oauth.redirectTo")} <Code>{client.redirectHost}</Code>
        </Text>
      </Stack>
      {answer.nameWarning && (
        <Alert color="orange" icon={<IconAlertTriangle size={16} />} title={t("oauth.nameWarningTitle")}>
          {t("oauth.nameWarning", { app: brand.title, host: client.clientIdHost })}
        </Alert>
      )}
      {client.loopback && (
        <Alert color="orange" icon={<IconAlertTriangle size={16} />} title={t("oauth.loopbackTitle")}>
          {t("oauth.loopback")}
        </Alert>
      )}
      {answer.firstUse && (
        <Alert color="blue" icon={<IconInfoCircle size={16} />}>
          {t("oauth.firstUse", { host: client.clientIdHost })}
        </Alert>
      )}
      {choices.some((choice) => choice.eligible) ? (
        <Select
          label={t("oauth.integration")}
          description={t("oauth.integrationHint")}
          placeholder={t("oauth.integrationPlaceholder")}
          data={choices.map((choice) => ({
            value: choice.id,
            label: choiceLabel(choice, t),
            disabled: !choice.eligible,
          }))}
          value={integrationId}
          onChange={(value) => {
            setIntegrationId(value);
            setAccess("read");
          }}
          allowDeselect={false}
          disabled={busy}
        />
      ) : (
        <Alert color="gray">{t("oauth.noIntegrations")}</Alert>
      )}
      <Radio.Group label={t("oauth.access")} value={access} onChange={(value) => setAccess(value as Access)}>
        <Stack gap="xs" mt="xs">
          <Radio value="read" label={t("oauth.accessRead")} disabled={busy} />
          <Radio
            value="read_write"
            label={t("oauth.accessReadWrite")}
            description={chosen && !writeAllowed ? t("oauth.writeUnavailable") : undefined}
            disabled={busy || !writeAllowed}
          />
        </Stack>
      </Radio.Group>
      <Text size="sm" c="dimmed">
        {t("oauth.terms")}
      </Text>
      {decide.isError && <Alert color="red">{failureText(decide.error, t)}</Alert>}
      <Group justify="flex-end">
        <Button
          variant="default"
          onClick={() => send("deny")}
          disabled={busy && decide.variables !== "deny"}
          loading={busy && decide.variables === "deny"}
        >
          {t("oauth.deny")}
        </Button>
        <Button
          onClick={() => send("approve")}
          disabled={!chosen || (busy && decide.variables !== "approve")}
          loading={busy && decide.variables === "approve"}
        >
          {t("oauth.approve")}
        </Button>
      </Group>
    </Frame>
  );
};

const OAuthAuthorizeRoute = () => {
  const query = authorizeQuery();
  // The router has rewritten the address by now; put the client's query back, so a reload reads it unchanged.
  useEffect(() => restoreAuthorizeAddress(query), [query]);
  return <OAuthAuthorizePage query={query} />;
};

export const Route = createFileRoute("/oauth/authorize")({
  component: OAuthAuthorizeRoute,
});
