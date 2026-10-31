import { Alert, Button, Stack } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { connectionQueryOptions, type EntraConnection } from "../api/connection";
import { callerIsOwnerQueryOptions } from "../api/session";
import "../i18n";

/**
 * What every tab shares: the connection and whether the caller is an Owner
 * (the queries are cached under one key, so four tabs ask once), the
 * skeleton while they load and the retry when the connection cannot be read.
 * The tab's own cards render once both answers are in.
 */
export function TabFrame({
  children,
}: {
  children: (state: { connection: EntraConnection; owner: boolean }) => ReactNode;
}) {
  const { t } = useI18n("entra");
  const connection = useQuery(connectionQueryOptions());
  const owner = useQuery(callerIsOwnerQueryOptions());

  return (
    <Stack gap="lg">
      {(connection.isPending || owner.isPending) && <ContentSkeleton rows={4} rowHeight={80} />}
      {connection.isError && (
        <Alert color="red" title={t("loadFailed")}>
          <Button size="compact-sm" variant="light" onClick={() => void connection.refetch()}>
            {t("tryAgain")}
          </Button>
        </Alert>
      )}
      {connection.data && !owner.isPending && children({ connection: connection.data, owner: owner.data === true })}
    </Stack>
  );
}
