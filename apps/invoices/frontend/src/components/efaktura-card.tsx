import { Badge, Button, Group, Stack, Table, Text, Title } from "@mantine/core";
import { IconSend } from "@tabler/icons-react";
import { PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { InvoiceDocument } from "../api/invoices";
import "../i18n";
import { useInvoiceFormat } from "../lib/format";
import { ResolveTransmissionModal } from "../pages/-resolve-transmission-modal";

export interface EfakturaCardProps {
  /** An issued invoice with its `efaktura` block. */
  document: InvoiceDocument;
  /** Whether the card offers Send as eFaktura. */
  offerSend: boolean;
  /** meta's `canIssue`: Resolve on an unconfirmed send is an issuer's. */
  canIssue?: boolean;
  onSend: () => void;
}

/**
 * The eFaktura card (invoices phase 6 design I31, I33): the latest
 * transmission's state, every transmission newest first with whether it went
 * with AvtaleGiro, and the AvtaleGiro claim's state, amount and due date.
 */
export const EfakturaCard = ({ document: doc, offerSend, onSend, canIssue = false }: EfakturaCardProps) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const [resolving, setResolving] = useState<number | null>(null);
  const efaktura = doc.efaktura;
  if (!efaktura) return null;
  const claim = efaktura.claim;
  return (
    <PageSection>
      <Stack gap="sm" data-testid="efaktura-card">
        <Group justify="space-between">
          <Title order={4}>{t("efaktura.title")}</Title>
          <Badge variant="light">{t(`efaktura.status.${efaktura.status}`)}</Badge>
        </Group>
        {claim && (
          <Stack gap={2} data-testid="avtalegiro-claim">
            <Text size="sm" fw={600}>
              {t("efaktura.claim", { state: t(`efaktura.claimStatus.${claim.status}`) })}
            </Text>
            <Text size="sm">
              {t("efaktura.claimFigures", { amount: money(claim.amount, "NOK"), due: date(claim.dueDate) })}
            </Text>
            {claim.attention && <Text size="sm">{t("efaktura.claimAttention")}</Text>}
            {claim.cancelRefusedAt && <Text size="sm">{t("efaktura.claimCancelRefused")}</Text>}
          </Stack>
        )}
        {efaktura.transmissions.length > 0 && (
          <Table>
            <Table.Tbody>
              {efaktura.transmissions.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Td>{t(`efaktura.status.${row.status}`)}</Table.Td>
                  <Table.Td>{t(`efaktura.paymentType.${row.paymentType}`)}</Table.Td>
                  <Table.Td>{dateTime(row.deliveredAt ?? row.submittedAt ?? row.queuedAt)}</Table.Td>
                  <Table.Td>
                    {canIssue && row.status === "unconfirmed" && (
                      <Button size="xs" variant="default" onClick={() => setResolving(row.id)}>
                        {t("efaktura.resolve")}
                      </Button>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        {offerSend && efaktura.canSend && (
          <Group>
            <Button variant="default" leftSection={<IconSend size={16} />} onClick={onSend}>
              {t("efaktura.send")}
            </Button>
          </Group>
        )}
      </Stack>
      {resolving !== null && (
        <ResolveTransmissionModal document={doc} transmission={{ id: resolving }} onClose={() => setResolving(null)} />
      )}
    </PageSection>
  );
};
