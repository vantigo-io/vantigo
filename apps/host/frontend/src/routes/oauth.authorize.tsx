import { Alert, Button, Card, Center, Code, Group, Loader, Radio, Select, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { AuthBrandMark, appUrl, useBrand, useI18n, vantigoTokens } from "@vantigo/frontend-shell";
import { type ReactNode, useEffect, useState } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import {
  decideAuthorization,
  type OAuthAuthorizationPreview,
  type OAuthIntegrationChoice,
  previewAuthorization,
} from "../api/oauth";
import { mfaEnrolmentDestination } from "../lib/mfa-enrolment-gate";
import { authorizeQuery, rememberOAuthReturn } from "../lib/oauth-return";
import "../i18n";

type Access = "read" | "read_write";

/** The page's ways out of itself, in one place the tests can watch. */
export const oauthPageNavigation = {
  assign: (to: string) => window.location.assign(to),
  closeWindow: () => window.close(),
};

/** Leaves the page: the tab when the client opened it, else the workspace. */
const close = () => {
  oauthPageNavigation.closeWindow();
  oauthPageNavigation.assign(appUrl("/"));
};

/** Sends the browser to an address the server answered with, never one of the page's own making. */
const go = (to: string) => oauthPageNavigation.assign(to);

const statusOf = (error: unknown) => (error as { status?: number } | null)?.status;
const codeOf = (error: unknown) => (error as { code?: string } | null)?.code;

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
      <Loader size="sm" aria-label={t("oauth.loading")} />
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

/**
 * The OAuth consent page (OAuth connections design C13, C14, A6–A8, A26). A
 * client sends the browser here with an authorization request. The page reads
 * the session itself: without one it stores its return and goes to sign in; an
 * Owner held at MFA enrolment goes to enrol and comes back the same way; a person
 * who is not an Owner is told to ask one, and nothing is fetched. An Owner's
 * request is judged by the server first, client and redirect URI before
 * anything else, and the page follows its answer exactly: a refused client is
 * never redirected to; an error goes back to the client at once only when the
 * server says so, else behind a "Return to <host>" button; otherwise the
 * client is shown as it calls itself beside the hosts Vantigo verified, and
 * the Owner picks the integration and the access, Read only unless they choose
 * more. The page is never framed: the server's headers forbid it, and the page
 * renders nothing inside a frame.
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
  const mustEnrol = session.data?.mfaEnrollmentRequired === true;
  const preview = useQuery({
    queryKey: ["oauth", "authorize", "preview", query],
    queryFn: () => previewAuthorization(query),
    enabled: !framed && isOwner && !mustEnrol && query !== "",
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });

  useEffect(() => {
    if (framed || session.isPending || session.isError) return;
    if (!session.data) {
      rememberOAuthReturn(query);
      oauthPageNavigation.assign(appUrl("/sign-in"));
    } else if (mustEnrol) {
      rememberOAuthReturn(query);
      oauthPageNavigation.assign(appUrl(mfaEnrolmentDestination));
    }
  }, [framed, session.isPending, session.isError, session.data, mustEnrol, query]);

  const answer = preview.data;
  const automatic = answer?.outcome === "redirect" && answer.automatic === true && !!answer.redirectTo;
  useEffect(() => {
    if (automatic && answer?.redirectTo) go(answer.redirectTo);
  }, [automatic, answer?.redirectTo]);

  if (framed) return null;
  if (session.isError)
    return (
      <Refused title={t("oauth.failedTitle")}>
        <Alert color="red">{t("oauth.failed")}</Alert>
      </Refused>
    );
  if (session.isPending || !session.data || mustEnrol) return <Waiting />;
  if (!isOwner)
    return (
      <Refused title={t("oauth.askOwnerTitle")}>
        <Text>{t("oauth.askOwner")}</Text>
      </Refused>
    );
  if (query === "")
    return (
      <Refused title={t("oauth.refusedTitle")}>
        <Alert color="red">{t("oauth.noRequest")}</Alert>
      </Refused>
    );
  if (preview.isPending || automatic) return <Waiting />;
  if (preview.isError) return <PreviewFailed error={preview.error} />;
  if (answer?.outcome === "redirect" && answer.redirectTo)
    return <ReturnWithError answer={answer} redirectTo={answer.redirectTo} />;
  if (answer?.outcome === "consent" && answer.client) return <Consent query={query} answer={answer} />;
  return (
    <Refused title={t("oauth.refusedTitle")}>
      <Text>{t("oauth.refused")}</Text>
      {answer?.message && (
        <Text size="sm" c="dimmed">
          {answer.message}
        </Text>
      )}
    </Refused>
  );
};

const PreviewFailed = ({ error }: { error: unknown }) => {
  const { t } = useI18n("host");
  const status = statusOf(error);
  const message =
    status === 404
      ? t("oauth.switchedOff")
      : status === 429
        ? t("oauth.tooMany")
        : status === 403
          ? t("oauth.forbidden")
          : t("oauth.failed");
  return (
    <Refused title={t("oauth.failedTitle")}>
      <Alert color="red">{message}</Alert>
    </Refused>
  );
};

/** An error the client is told about, behind a click: the server did not judge its host safe to send to unasked (A26). */
const ReturnWithError = ({ answer, redirectTo }: { answer: OAuthAuthorizationPreview; redirectTo: string }) => {
  const { t } = useI18n("host");
  const host = answer.returnHost ?? "";
  return (
    <Frame>
      <Title order={2}>{t("oauth.requestErrorTitle")}</Title>
      <Text>{t("oauth.requestError")}</Text>
      {answer.message && (
        <Text size="sm" c="dimmed">
          {answer.message}
        </Text>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={close}>
          {t("oauth.close")}
        </Button>
        <Button onClick={() => go(redirectTo)}>{t("oauth.returnTo", { host })}</Button>
      </Group>
    </Frame>
  );
};

const choiceLabel = (choice: OAuthIntegrationChoice, t: (key: string) => string) =>
  choice.eligible || !choice.reason ? choice.name : `${choice.name} — ${t(`oauth.notEligible_${choice.reason}`)}`;

const Consent = ({ query, answer }: { query: string; answer: OAuthAuthorizationPreview }) => {
  const { t } = useI18n("host");
  const brand = useBrand();
  const client = answer.client as NonNullable<OAuthAuthorizationPreview["client"]>;
  const choices = answer.integrations ?? [];
  // No integration is preselected and access starts at Read only (A7).
  const [integrationId, setIntegrationId] = useState<string | null>(null);
  const [access, setAccess] = useState<Access>("read");
  const chosen = choices.find((choice) => choice.id === integrationId && choice.eligible);
  const writeAllowed = chosen?.maxAccess === "read_write";
  const decide = useMutation({
    mutationFn: (decision: "approve" | "deny") =>
      decideAuthorization(
        decision === "approve" && chosen
          ? { query, decision, integrationId: chosen.id, access: writeAllowed ? access : "read" }
          : { query, decision },
      ),
    onSuccess: (result) => go(result.redirectTo),
  });
  const refused = decide.isError && codeOf(decide.error) === "client_refused";
  if (refused)
    return (
      <Refused title={t("oauth.refusedTitle")}>
        <Text>{t("oauth.refused")}</Text>
      </Refused>
    );
  const busy = decide.isPending || decide.isSuccess;

  return (
    <Frame>
      <Title order={2}>{t("oauth.title", { app: brand.title })}</Title>
      <Stack gap={4}>
        <Text size="sm" c="dimmed">
          {t("oauth.callsItself")}
        </Text>
        <Text fw={700} size="lg" data-testid="oauth-client-name">
          {client.name}
        </Text>
      </Stack>
      <Stack gap={4} data-testid="oauth-verified">
        <Text size="sm">
          {client.loopback ? t("oauth.documentAtLoopback") : t("oauth.documentAt")} <Code>{client.clientIdHost}</Code>
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
      {decide.isError && (
        <Alert color="red">
          {codeOf(decide.error) === "integration_not_eligible"
            ? t("oauth.notEligible")
            : statusOf(decide.error) === 429
              ? t("oauth.tooMany")
              : t("oauth.failed")}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button
          variant="default"
          onClick={() => decide.mutate("deny")}
          disabled={busy && decide.variables !== "deny"}
          loading={busy && decide.variables === "deny"}
        >
          {t("oauth.deny")}
        </Button>
        <Button
          onClick={() => decide.mutate("approve")}
          disabled={!chosen || (busy && decide.variables !== "approve")}
          loading={busy && decide.variables === "approve"}
        >
          {t("oauth.approve")}
        </Button>
      </Group>
    </Frame>
  );
};

const OAuthAuthorizeRoute = () => <OAuthAuthorizePage query={authorizeQuery()} />;

export const Route = createFileRoute("/oauth/authorize")({
  component: OAuthAuthorizeRoute,
});
