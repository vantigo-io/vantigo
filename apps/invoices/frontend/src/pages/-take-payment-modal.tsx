import { Alert, Anchor, Button, Center, Loader, Modal, Stack, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { appUrl } from "@vantigo/frontend-shell";
import { useEffect, useRef, useState } from "react";
import { cancelPaymentRequest, createPaymentRequest, readPaymentRequest } from "../api/attempts";
import type { InvoiceDocument } from "../api/invoices";
import "../i18n";
import { refusalCode, refusalMessage, refusalProblem } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { QrScreen } from "./-qr-screen";

export interface TakePaymentModalProps {
  invoice: InvoiceDocument;
  onClose: () => void;
}

/** Where the modal stands: asking, showing a request's QR, or saying why there is none. */
type Step =
  | { kind: "asking" }
  | { kind: "qr"; attemptId: number; qrSvg?: string; notice?: string }
  | { kind: "refused"; words: string; customerLink?: boolean; staleAttemptId?: number };

/**
 * "Take a Vipps payment now" (invoices payments and reminders design D17,
 * D22): asks for an on-site request of the invoice's whole open amount at
 * once and shows its QR code full screen, polled until it is over. Every
 * refusal is said in words; `buyer_identity_missing` links to the customer,
 * whose details the invoice needs. On `payment_request_active` the request
 * named by `attemptId` is read: while it is `created` its QR code is shown
 * again and nothing new is made; otherwise the person may cancel it and make
 * a new one — and a request that cannot be cancelled, parked or approved, is
 * left to a manager to abandon.
 */
export const TakePaymentModal = ({ invoice, onClose }: TakePaymentModalProps) => {
  const { t, date } = useInvoiceFormat();
  const navigate = useNavigate() as (options: unknown) => void;
  const [step, setStep] = useState<Step>({ kind: "asking" });
  const started = useRef(false);

  const refused = (error: unknown, staleAttemptId?: number): Step => ({
    kind: "refused",
    words: refusalMessage(error, t, date),
    customerLink: refusalCode(error) === "buyer_identity_missing",
    staleAttemptId,
  });

  const ask = async () => {
    setStep({ kind: "asking" });
    try {
      const made = await createPaymentRequest(invoice.id);
      setStep({ kind: "qr", attemptId: made.attemptId, qrSvg: made.qrSvg });
    } catch (error) {
      const attemptId = refusalProblem(error).attemptId;
      if (refusalCode(error) !== "payment_request_active" || typeof attemptId !== "number") {
        setStep(refused(error));
        return;
      }
      // A request is open already — perhaps this tablet's own, whose answer
      // was lost. Its code is shown again while the customer has not answered.
      try {
        const open = await readPaymentRequest(invoice.id, attemptId);
        if (open.state === "created" && open.qrSvg) {
          setStep({ kind: "qr", attemptId, qrSvg: open.qrSvg, notice: t("takePayment.activeShownAgain") });
          return;
        }
      } catch {
        // Unreadable: offered the cancel all the same.
      }
      setStep({ kind: "refused", words: t("takePayment.activeNotShowable"), staleAttemptId: attemptId });
    }
  };

  const cancelAndRetry = async (attemptId: number) => {
    setStep({ kind: "asking" });
    try {
      await cancelPaymentRequest(invoice.id, attemptId);
    } catch (error) {
      setStep(
        refusalCode(error) === "payment_request_active"
          ? { kind: "refused", words: t("takePayment.parked") }
          : refused(error),
      );
      return;
    }
    await ask();
  };

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void ask();
  }, []);

  const customerHref = `/customers/${invoice.customerId}`;
  return (
    <Modal opened onClose={onClose} title={t("takePayment.title")} fullScreen>
      {step.kind === "asking" && (
        <Stack align="center" py="xl">
          <Loader />
          <Text>{t("takePayment.asking")}</Text>
        </Stack>
      )}
      {step.kind === "qr" && (
        <QrScreen
          invoiceId={invoice.id}
          attemptId={step.attemptId}
          qrSvg={step.qrSvg}
          notice={step.notice}
          onClose={onClose}
        />
      )}
      {step.kind === "refused" && (
        <Center py="xl">
          <Stack maw={560}>
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("takePayment.couldNot")}>
              <Stack gap="xs">
                <Text size="sm">{step.words}</Text>
                {step.customerLink && (
                  <Anchor
                    size="sm"
                    href={appUrl(customerHref)}
                    onClick={(event) => {
                      if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
                      event.preventDefault();
                      navigate({ to: customerHref });
                    }}
                  >
                    {t("takePayment.openCustomer")}
                  </Anchor>
                )}
              </Stack>
            </Alert>
            {step.staleAttemptId !== undefined && (
              <Button onClick={() => void cancelAndRetry(step.staleAttemptId as number)}>
                {t("takePayment.cancelAndRetry")}
              </Button>
            )}
            <Button variant="default" onClick={onClose}>
              {t("qr.close")}
            </Button>
          </Stack>
        </Center>
      )}
    </Modal>
  );
};
