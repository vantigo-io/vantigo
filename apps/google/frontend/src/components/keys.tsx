import { Badge, Button, FileButton, Group, Stack, Table, Text, Textarea } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconFileUpload } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type GoogleCredential, revokeCredential, uploadKey } from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { fieldRefusals, refusalText } from "../lib/words";

/** The server's own cap on a key file (Google Workspace design G13). */
const maxKeyBytes = 8 * 1024;

/** A chosen file's text, read in the browser: the key never travels as a file, only as the text the Owner chose. */
const readText = (file: File): Promise<string> =>
  typeof file.text === "function"
    ? file.text()
    : new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result ?? ""));
        reader.onerror = () => reject(reader.error);
        reader.readAsText(file);
      });

/** Google refused the key after it last fetched a token, or it never fetched one: a key working again is not shown refused. */
const refusedSinceUsed = (key: GoogleCredential): key is GoogleCredential & { lastFailedAt: string } =>
  key.lastFailedAt != null && (key.lastUsedAt == null || Date.parse(key.lastFailedAt) > Date.parse(key.lastUsedAt));

/**
 * The service account's keys, revoked ones included, newest first: per key
 * only the four things the server keeps in the clear that a person needs —
 * the service account's address, its numeric client id, the key's id and
 * when it was added. The key itself is never answered, so never shown.
 */
export const KeyList = ({ credentials, owner }: { credentials: GoogleCredential[]; owner: boolean }) => {
  const { t, formatters } = useI18n("google");
  const queryClient = useQueryClient();
  const keys = credentials.filter((credential) => credential.kind === "service_account_key");
  const date = (value: string) => formatters.formatDate(new Date(value), { dateStyle: "medium" });
  const nameOf = (key: GoogleCredential) => key.privateKeyId ?? t("keyUnnamed", { id: key.id });

  const revoke = useMutation({
    mutationFn: (key: GoogleCredential) => revokeCredential(key.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("keyRevoked"), message: t("keyRevokedBody") });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotRevoke"), message: refusalText(error, t) }),
  });

  const askRevoke = (key: GoogleCredential) =>
    modals.openConfirmModal({
      title: t("revokeKeyTitle", { name: nameOf(key) }),
      children: <Text size="sm">{t("revokeKeyConfirm")}</Text>,
      labels: { confirm: t("revoke"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => revoke.mutate(key),
    });

  if (keys.length === 0)
    return (
      <Text size="sm" c="dimmed">
        {t("noKeys")}
      </Text>
    );

  return (
    <Table.ScrollContainer minWidth={640}>
      <Table verticalSpacing="xs" aria-label={t("keys")}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("keyServiceAccount")}</Table.Th>
            <Table.Th>{t("keyClientId")}</Table.Th>
            <Table.Th>{t("keyId")}</Table.Th>
            <Table.Th>{t("keyAdded")}</Table.Th>
            <Table.Th>{t("keyState")}</Table.Th>
            {owner && <Table.Th aria-label={t("actions")} />}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {keys.map((key) => (
            <Table.Tr key={key.id}>
              <Table.Td style={{ wordBreak: "break-all" }}>{key.clientEmail ?? "—"}</Table.Td>
              <Table.Td>{key.clientId ?? "—"}</Table.Td>
              <Table.Td style={{ wordBreak: "break-all" }}>{key.privateKeyId ?? "—"}</Table.Td>
              <Table.Td>{date(key.createdAt)}</Table.Td>
              <Table.Td>
                <Stack gap={2} align="flex-start">
                  {key.revokedAt ? (
                    <Badge color="gray" variant="light">
                      {t("revokedOn", { date: date(key.revokedAt) })}
                    </Badge>
                  ) : key.inUse ? (
                    <Badge color="teal">{t("inUse")}</Badge>
                  ) : (
                    <Badge color="gray" variant="outline">
                      {t("notInUse")}
                    </Badge>
                  )}
                  {refusedSinceUsed(key) && !key.revokedAt && (
                    <Text size="xs" c="red">
                      {t("keyRefusedOn", { date: date(key.lastFailedAt) })}
                    </Text>
                  )}
                </Stack>
              </Table.Td>
              {owner && (
                <Table.Td>
                  {!key.revokedAt && (
                    <Button
                      size="compact-sm"
                      variant="subtle"
                      color="red"
                      aria-label={t("revokeNamed", { name: nameOf(key) })}
                      loading={revoke.isPending && revoke.variables?.id === key.id}
                      onClick={() => askRevoke(key)}
                    >
                      {t("revoke")}
                    </Button>
                  )}
                </Table.Td>
              )}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

/**
 * *Upload the key*: the JSON key pasted, or a chosen `.json` file read here
 * in the browser; either way the file's text is sent as `{json}`. The text
 * leaves the page with the answer and is never shown back.
 */
export const UploadKeyForm = () => {
  const { t } = useI18n("google");
  const queryClient = useQueryClient();
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);

  const upload = useMutation({
    mutationFn: (keyText: string) => uploadKey(keyText),
    onSuccess: async () => {
      setText("");
      setError(null);
      notifications.show({ color: "teal", title: t("keyUploaded"), message: t("keyUploadedBody") });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY] });
    },
    onError: (refusal) => {
      const { fields, rest } = fieldRefusals(refusal, ["json"], t);
      setError(fields.json ?? rest.join(" "));
    },
  });

  const send = (keyText: string) => {
    if (keyText.trim().length === 0) {
      setError(t("keyRequired"));
      return;
    }
    if (new TextEncoder().encode(keyText).length > maxKeyBytes) {
      setError(t("keyTooLarge"));
      return;
    }
    setError(null);
    upload.mutate(keyText);
  };

  const choose = async (file: File | null) => {
    if (!file) return;
    if (file.size > maxKeyBytes) {
      setError(t("keyTooLarge"));
      return;
    }
    try {
      send(await readText(file));
    } catch {
      setError(t("keyUnreadable"));
    }
  };

  return (
    <form
      aria-label={t("uploadKey")}
      onSubmit={(event) => {
        event.preventDefault();
        send(text);
      }}
    >
      <Stack gap="sm">
        <Textarea
          label={t("keyPaste")}
          description={t("keyPasteDescription")}
          rows={5}
          spellCheck={false}
          autoComplete="off"
          value={text}
          error={error}
          onChange={(event) => setText(event.currentTarget.value)}
        />
        <Group justify="space-between">
          <FileButton onChange={(file) => void choose(file)} accept=".json,application/json">
            {(props) => (
              <Button
                {...props}
                variant="default"
                leftSection={<IconFileUpload size={16} />}
                loading={upload.isPending}
              >
                {t("keyChooseFile")}
              </Button>
            )}
          </FileButton>
          <Button type="submit" loading={upload.isPending}>
            {t("uploadKeyButton")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
