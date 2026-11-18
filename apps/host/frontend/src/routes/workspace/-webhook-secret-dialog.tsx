import { Alert, Button, Modal, Select, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  rollWebhookSecret,
  type WebhookEndpoint,
  type WebhookEndpointSecret,
  webhooksQueryKey,
} from "../../api/webhooks";
import { fieldErrorsOf } from "./-api-access-shared";
import { Copyable } from "./-api-key-dialog";

/**
 * An endpoint's signing secret, just made or rolled (outgoing webhooks design
 * W8): shown this once, with Copy and the warning that it cannot be shown
 * again. It lives in this component's props only, never in a query or in
 * storage.
 */
export const WebhookSecretPanel = ({ secret, onDone }: { secret: WebhookEndpointSecret; onDone: () => void }) => {
  const { t } = useI18n("host");
  return (
    <Stack>
      <Alert color="orange" icon={<IconAlertTriangle size={16} />} title={t("apiAccess.webhookSecretTitle")}>
        {t("apiAccess.webhookSecretWarning")}
      </Alert>
      <Copyable label={t("apiAccess.webhookSecret")} value={secret.secret} />
      <Text size="sm" c="dimmed">
        {t("apiAccess.webhookSecretHint")}
      </Text>
      <Button onClick={onDone}>{t("apiAccess.webhookSecretDone")}</Button>
    </Stack>
  );
};

/**
 * The dialog that shows a new endpoint's secret: only *I have copied the
 * secret* closes it, and closing it forgets the secret.
 */
export const WebhookSecretDialog = ({
  secret,
  onClose,
}: {
  secret: WebhookEndpointSecret | null;
  onClose: () => void;
}) => {
  const { t } = useI18n("host");
  return (
    <Modal
      opened={secret !== null}
      onClose={onClose}
      title={t("apiAccess.webhookSecret")}
      size="lg"
      closeOnClickOutside={false}
      closeOnEscape={false}
      withCloseButton={false}
      centered
    >
      {secret && <WebhookSecretPanel secret={secret} onDone={onClose} />}
    </Modal>
  );
};

/** The overlaps a roll offers, in hours (W9): 1 hour to 7 days, 24 hours by default. */
const overlapChoices = [1, 24, 72, 168] as const;

/**
 * *Roll secret* (W9): a new secret, the one it replaces signing beside it for
 * the overlap; a warning when an overlap still runs, since rolling again ends
 * the older secret at once; then the new secret, shown once.
 */
export const RollWebhookSecretDialog = ({
  endpoint,
  integrationId,
  onClose,
}: {
  endpoint: WebhookEndpoint | null;
  integrationId: string;
  onClose: () => void;
}) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const [overlap, setOverlap] = useState("24");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [secret, setSecret] = useState<WebhookEndpointSecret | null>(null);
  const roll = useMutation({
    gcTime: 0,
    mutationFn: (one: WebhookEndpoint) => rollWebhookSecret(integrationId, one.id, Number(overlap)),
    onSuccess: async (made) => {
      setSecret(made);
      await queryClient.invalidateQueries({ queryKey: webhooksQueryKey(integrationId) });
    },
    onError: (error) => {
      setErrors(fieldErrorsOf(error));
      notifications.show({
        color: "red",
        title: t("apiAccess.webhookCouldNotRoll"),
        message: (error as Error).message,
      });
    },
  });
  const close = () => {
    setSecret(null);
    roll.reset();
    setOverlap("24");
    setErrors({});
    onClose();
  };
  return (
    <Modal
      opened={endpoint !== null}
      onClose={close}
      title={secret ? t("apiAccess.webhookSecret") : t("apiAccess.webhookRollTitle")}
      size="lg"
      closeOnClickOutside={secret === null}
      closeOnEscape={secret === null}
      withCloseButton={secret === null}
      centered
    >
      {secret ? (
        <WebhookSecretPanel secret={secret} onDone={close} />
      ) : (
        endpoint && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              roll.mutate(endpoint);
            }}
          >
            <Stack>
              <Text size="sm">{t("apiAccess.webhookRollBody")}</Text>
              {endpoint.previousSecretExpiresAt && (
                <Alert color="orange" icon={<IconAlertTriangle size={16} />}>
                  {t("apiAccess.webhookRollRunning")}
                </Alert>
              )}
              <Select
                label={t("apiAccess.overlap")}
                description={t("apiAccess.webhookOverlapHint")}
                allowDeselect={false}
                data={overlapChoices.map((hours) => ({
                  value: String(hours),
                  label: t(`apiAccess.overlap_${hours}`),
                }))}
                value={overlap}
                error={errors.overlapHours}
                onChange={(value) => setOverlap(value ?? "24")}
              />
              <Button type="submit" loading={roll.isPending}>
                {t("apiAccess.webhookRoll")}
              </Button>
            </Stack>
          </form>
        )
      )}
    </Modal>
  );
};
