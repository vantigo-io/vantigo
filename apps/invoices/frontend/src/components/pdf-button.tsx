import { Button, type ButtonProps } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { type ReactNode, useState } from "react";
import { fetchPdf } from "../api/invoices";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface PdfButtonProps extends ButtonProps {
  /** Where the PDF is: pdfUrl for a download, previewUrl for a preview. */
  url: string;
  /** "download" saves the file under the server's name; "open" shows it in a new tab. */
  mode: "download" | "open";
  children: ReactNode;
}

/**
 * A PDF behind a button rather than a bare link: the PDF is fetched first, so
 * a refusal — a draft's download, a store that is down — is a notification in
 * the reader's language, never the problem JSON opened in the browser.
 */
export const PdfButton = ({ url, mode, children, ...button }: PdfButtonProps) => {
  const { t, date } = useInvoiceFormat();
  const [loading, setLoading] = useState(false);
  const open = async () => {
    // A tab opened after the fetch would be a pop-up the browser blocks: it is
    // opened now, with the click, and pointed at the PDF once it is here.
    const tab = mode === "open" ? window.open("", "_blank") : null;
    setLoading(true);
    try {
      const pdf = await fetchPdf(url);
      const objectUrl = URL.createObjectURL(pdf.blob);
      if (tab) {
        tab.location.href = objectUrl;
      } else {
        const anchor = document.createElement("a");
        anchor.href = objectUrl;
        anchor.download = pdf.fileName ?? "document.pdf";
        anchor.click();
      }
      setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000);
    } catch (error) {
      tab?.close();
      notifications.show({ color: "red", title: t("couldNotOpenPdf"), message: refusalMessage(error, t, date) });
    } finally {
      setLoading(false);
    }
  };
  return (
    <Button {...button} loading={loading} onClick={open}>
      {children}
    </Button>
  );
};
