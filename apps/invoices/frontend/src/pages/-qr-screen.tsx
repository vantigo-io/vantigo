import { Alert, Button, Center, Group, Image, Loader, Stack, Text, Title } from "@mantine/core";
import { IconAlertCircle, IconCircleCheck } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import {
  cancelPaymentRequest,
  FINISHED_ATTEMPT_STATES,
  type OnSitePaymentStatus,
  paymentRequestQueryOptions,
} from "../api/attempts";
import { INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { svgDataUrl } from "../lib/qr";

/** How often the request's state is read while the customer answers: every two seconds (D17). */
export const QR_POLL_MS = 2000;

export interface QrScreenProps {
  invoiceId: number;
  attemptId: number;
  /** The QR code the request's create — or a read of it — answered, until the first poll answers its own. */
  qrSvg?: string;
  /** A line above the code: the request already open, shown again. */
  notice?: string;
  onClose: () => void;
}

/**
 * An on-site Vipps payment request, full screen for the customer at the
 * counter (invoices payments and reminders design D17, D22): its QR code — an
 * image of the server's own SVG, never inlined, never printed — while the
 * request is `created`, its state read every two seconds until it is over,
 * and "Cancel the request" while the customer has not answered. A request
 * whose answer was lost shows the code the read answers again while it is
 * still `created`. Once paid, every invoices query is read again, so the
 * invoice shows its payment. The quick invoice's screen uses it too.
 */
export const QrScreen = ({ invoiceId, attemptId, qrSvg, notice, onClose }: QrScreenProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [refused, setRefused] = useState<string | null>(null);
  const options = paymentRequestQueryOptions(invoiceId, attemptId);
  const status = useQuery({
    ...options,
    refetchInterval: (query) =>
      query.state.data && FINISHED_ATTEMPT_STATES.includes(query.state.data.state) ? false : QR_POLL_MS,
    refetchIntervalInBackground: true,
  });
  const shown: OnSitePaymentStatus = status.data ?? { state: "created", qrSvg };
  const finished = FINISHED_ATTEMPT_STATES.includes(shown.state);
  const code = shown.state === "created" ? (shown.qrSvg ?? (status.data ? undefined : qrSvg)) : undefined;
  useEffect(() => {
    // Everything else is read again — the invoice shows its payment — but not
    // this request, which is over: the poll ends with the capture.
    if (shown.state === "captured") {
      void queryClient.invalidateQueries({
        queryKey: [INVOICES_QUERY_KEY],
        predicate: (query) => query.queryKey[1] !== "payment-request",
      });
    }
  }, [shown.state, queryClient]);
  const cancel = useMutation({
    mutationFn: () => cancelPaymentRequest(invoiceId, attemptId),
    onMutate: () => setRefused(null),
    // The cancel's answer is the request's state now; the poll goes on from it
    // while it is still being cancelled.
    onSuccess: async (answer) => {
      await queryClient.cancelQueries({ queryKey: options.queryKey });
      queryClient.setQueryData(options.queryKey, answer);
    },
    onError: (error) => setRefused(refusalMessage(error, t, date)),
  });
  return (
    <Stack align="center" gap="md" py="md">
      {notice && <Text size="sm">{notice}</Text>}
      <Title order={3} data-testid="qr-state">
        {t(`attempt.state.${shown.state}`)}
      </Title>
      {code && (
        <>
          <Image src={svgDataUrl(code)} alt={t("qr.alt")} w={320} h={320} fit="contain" />
          <Text size="sm" ta="center">
            {t("qr.scan")}
          </Text>
        </>
      )}
      {shown.state === "created" && !code && (
        <Center>
          <Loader size="sm" />
        </Center>
      )}
      {(shown.state === "authorized" || shown.state === "capturing" || shown.state === "capture_unknown") && (
        <Text>{t("qr.taking")}</Text>
      )}
      {shown.state === "captured" && (
        <Alert color="green" icon={<IconCircleCheck size={16} />}>
          {t("qr.paid")}
        </Alert>
      )}
      {finished && shown.state !== "captured" && <Text>{t("qr.ended")}</Text>}
      {status.isError && !finished && (
        <Text size="sm" c="dimmed">
          {t("qr.couldNotRead")}
        </Text>
      )}
      {refused && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("qr.couldNotCancel")}>
          {refused}
        </Alert>
      )}
      <Group>
        {shown.state === "created" && (
          <Button variant="default" color="red" loading={cancel.isPending} onClick={() => cancel.mutate()}>
            {t("qr.cancel")}
          </Button>
        )}
        {finished && <Button onClick={onClose}>{t("qr.close")}</Button>}
      </Group>
    </Stack>
  );
};
