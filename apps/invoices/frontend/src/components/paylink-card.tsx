import { Alert, Button, Card, CopyButton, Group, Image, Stack, Text, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconCopy, IconLink, IconLinkOff } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { InvoiceDocument } from "../api/invoices";
import { makePayLink, revokePayLink } from "../api/paylink";
import { INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { svgDataUrl } from "../lib/qr";

export interface PayLinkCardProps {
  /** An issued invoice: a credit note has no pay link. */
  invoice: InvoiceDocument;
  /** meta's `canIssue` — `invoices:issue`, which making and revoking a link needs. */
  canIssue: boolean;
  /** meta's `payLinksAvailable`: a new link can be made only while it is true. */
  payLinksAvailable: boolean;
}

/**
 * An issued invoice's pay link (invoices payments and reminders design D15,
 * D22): when it was made, and — only when the server answered them, which it
 * does to `invoices:issue` while payment on site is available (I5, I21) — its
 * address, its QR code (an image of the server's own SVG, never inlined, never
 * drawn here) and "Copy link". Nothing is derived on the client: without the
 * server's `url` the card says why no link is on screen. "Make a pay link"
 * for `invoices:issue` while pay links are available and the invoice has
 * none; "Revoke the link" after asking. Shown while the invoice has a link or
 * one could be made.
 */
export const PayLinkCard = ({ invoice, canIssue, payLinksAvailable }: PayLinkCardProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const link = invoice.payLink;
  const refresh = () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const make = useMutation({
    mutationFn: () => makePayLink(invoice.id),
    onSuccess: async () => {
      await refresh();
      notifications.show({ color: "green", message: t("payLink.made") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("payLink.couldNotMake"), message: refusalMessage(error, t, date) }),
  });
  const revoke = useMutation({
    mutationFn: () => revokePayLink(invoice.id),
    onSuccess: async () => {
      await refresh();
      notifications.show({ color: "green", message: t("payLink.revoked") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("payLink.couldNotRevoke"), message: refusalMessage(error, t, date) }),
  });
  if (!link && !payLinksAvailable) return null;
  const confirmRevoke = () =>
    modals.openConfirmModal({
      title: t("payLink.revokeTitle"),
      children: <Text size="sm">{t("payLink.revokeConfirm")}</Text>,
      labels: { confirm: t("payLink.revoke"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => revoke.mutate(),
    });
  return (
    <Card withBorder data-testid="pay-link-card">
      <Stack gap="xs">
        <Group justify="space-between">
          <Title order={4}>{t("payLink.title")}</Title>
          {canIssue && link && (
            <Button
              size="xs"
              variant="default"
              color="red"
              leftSection={<IconLinkOff size={14} />}
              loading={revoke.isPending}
              onClick={confirmRevoke}
            >
              {t("payLink.revoke")}
            </Button>
          )}
          {canIssue && !link && payLinksAvailable && (
            <Button
              size="xs"
              leftSection={<IconLink size={14} />}
              loading={make.isPending}
              onClick={() => make.mutate()}
            >
              {t("payLink.make")}
            </Button>
          )}
        </Group>
        {!link && (
          <Text size="sm" c="dimmed">
            {t("payLink.none")}
          </Text>
        )}
        {link && <Text size="sm">{t("payLink.madeAt", { at: dateTime(link.createdAt) })}</Text>}
        {link?.url ? (
          <Group align="flex-start" wrap="nowrap">
            {link.qrSvg && (
              <Image src={svgDataUrl(link.qrSvg)} alt={t("payLink.qrAlt")} w={160} h={160} fit="contain" />
            )}
            <Stack gap="xs" style={{ minWidth: 0 }}>
              <Text size="sm" data-testid="pay-link-url" style={{ wordBreak: "break-all" }}>
                {link.url}
              </Text>
              <CopyButton value={link.url}>
                {({ copied, copy }) => (
                  <Button size="xs" variant="default" leftSection={<IconCopy size={14} />} onClick={copy}>
                    {copied ? t("payLink.copied") : t("payLink.copy")}
                  </Button>
                )}
              </CopyButton>
            </Stack>
          </Group>
        ) : (
          link && (
            <Alert color="gray" variant="light">
              {t("payLink.notOnScreen")}
            </Alert>
          )
        )}
      </Stack>
    </Card>
  );
};
