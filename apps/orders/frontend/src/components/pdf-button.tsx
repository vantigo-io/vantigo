import { Button, type ButtonProps } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { type ReactNode, useState } from "react";
import { fetchPdf } from "../api/pdf";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";

export interface PdfButtonProps extends ButtonProps {
  url: string;
  /** "download" saves the file; "open" shows it in a new tab. */
  mode?: "download" | "open";
  children: ReactNode;
}

/**
 * A PDF behind a button (O24): fetched first, so a refusal is a notification
 * in the reader's language, never the problem JSON in a tab.
 */
export const PdfButton = ({ url, mode = "open", children, ...button }: PdfButtonProps) => {
  const { t } = useOrdersFormat();
  const [loading, setLoading] = useState(false);
  const open = async () => {
    // A tab opened after the fetch would be a blocked pop-up: it opens with the click.
    const tab = mode === "open" ? window.open("", "_blank") : null;
    setLoading(true);
    try {
      const pdf = await fetchPdf(url);
      const objectUrl = URL.createObjectURL(pdf.blob);
      if (tab) tab.location.href = objectUrl;
      else {
        const anchor = document.createElement("a");
        anchor.href = objectUrl;
        anchor.download = pdf.fileName ?? t("pdfFileName");
        anchor.click();
      }
      setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000);
    } catch (error) {
      tab?.close();
      notifications.show({ color: "red", title: t("couldNotOpenPdf"), message: refusalMessage(error, t) });
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
