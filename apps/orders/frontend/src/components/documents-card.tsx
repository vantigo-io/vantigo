import { Badge, Group, Stack, Table, Text, Title } from "@mantine/core";
import { IconFileTypePdf } from "@tabler/icons-react";
import { PageSection } from "@vantigo/frontend-shell";
import type { Mailing, StoredDocument } from "../api/types";
import { hasWords } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { PdfButton } from "./pdf-button";

const mailingColour: Record<string, string> = { queued: "gray", sent: "green", failed: "red" };

export interface DocumentsCardProps {
  kind: "quote" | "order";
  documents?: StoredDocument[];
  mailings?: Mailing[];
  /** Where the stored PDF of a revision (a quote) or a version (an order) is. */
  pdfUrl: (version: number) => string;
}

/**
 * The stored PDFs (O24) — each sent revision of a quote, each sent or
 * confirmed version of an order — and every send with its status (O25).
 */
export const DocumentsCard = ({ kind, documents = [], mailings = [], pdfUrl }: DocumentsCardProps) => {
  const { t, dateTime } = useOrdersFormat();
  return (
    <PageSection data-testid="documents-card">
      <Stack gap="md">
        <Title order={4}>{t("documentsAndMailings")}</Title>
        {documents.length === 0 ? (
          <Text size="sm" c="dimmed">
            {kind === "quote" ? t("noStoredQuotes") : t("noStoredConfirmations")}
          </Text>
        ) : (
          <Stack gap={4}>
            {documents.map((document) => (
              <Group key={document.id} justify="space-between" wrap="nowrap">
                <Group gap="xs" wrap="nowrap">
                  <IconFileTypePdf size={18} />
                  <Text size="sm">
                    {kind === "quote"
                      ? t("storedRevision", { revision: document.version })
                      : t("storedVersion", { version: document.version })}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {dateTime(document.createdAt)}
                  </Text>
                </Group>
                <PdfButton size="compact-xs" variant="subtle" url={pdfUrl(document.version)}>
                  {t("open")}
                </PdfButton>
              </Group>
            ))}
          </Stack>
        )}
        <Title order={5}>{t("mailings")}</Title>
        {mailings.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noMailings")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={520}>
            <Table verticalSpacing={4} data-testid="mailings">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("recipient")}</Table.Th>
                  <Table.Th>{kind === "quote" ? t("revision") : t("version")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("when")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {mailings.map((mailing) => (
                  <Table.Tr key={mailing.id}>
                    <Table.Td>
                      <Text size="sm">{mailing.recipient || t("recipientErased")}</Text>
                      {mailing.cc.length > 0 && (
                        <Text size="xs" c="dimmed">
                          {t("ccList", { addresses: mailing.cc.join(", ") })}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>{mailing.version}</Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={mailingColour[mailing.status] ?? "gray"}>
                        {hasWords(`mailingStatus.${mailing.status}`)
                          ? t(`mailingStatus.${mailing.status}`)
                          : mailing.status}
                      </Badge>
                      {mailing.lastError && (
                        <Text size="xs" c="red">
                          {mailing.lastError}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs">{dateTime(mailing.sentAt ?? mailing.createdAt)}</Text>
                      {mailing.sentBy && (
                        <Text size="xs" c="dimmed">
                          {mailing.sentBy.displayName}
                        </Text>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </PageSection>
  );
};
