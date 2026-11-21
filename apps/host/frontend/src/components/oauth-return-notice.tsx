import { Alert, Button, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { oauthReturnNavigation, peekOAuthReturn, takeOAuthReturn, touchOAuthReturn } from "../lib/oauth-return";

/**
 * The way back to an app's connection request after a forced MFA enrolment
 * (OAuth connections design A8, A27). The security page holds an Owner who
 * must enrol; while it does, a waiting `oauth.return` entry has its ten minutes
 * started again, so the time spent enrolling does not use it up. Once the
 * session no longer needs enrolment, this offers a button — never an automatic
 * navigation, which would take the person away from their recovery codes —
 * that takes the entry and loads the consent page again. It shows nothing when
 * no valid entry waits; looking does not consume one.
 */
export const OAuthReturnNotice = () => {
  const { t } = useI18n("host");
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const gated = session.data?.mfaEnrollmentRequired === true;
  const [taken, setTaken] = useState(false);
  useEffect(() => {
    if (gated) touchOAuthReturn();
  }, [gated]);
  if (!session.data || gated || taken || peekOAuthReturn() === undefined) return null;
  const go = () => {
    const back = takeOAuthReturn();
    setTaken(true);
    if (back) oauthReturnNavigation.assign(appUrl(back));
  };
  return (
    <Alert color="blue" title={t("oauth.continueTitle")}>
      <Stack gap="sm">
        <Text size="sm">{t("oauth.continueBody")}</Text>
        <Button w="fit-content" onClick={go}>
          {t("oauth.continue")}
        </Button>
      </Stack>
    </Alert>
  );
};
