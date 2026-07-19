import { notFound } from "next/navigation";
import { ContactDetail } from "./contact-detail";

export default async function Page({
  params,
}: Readonly<{ params: Promise<{ contactId: string }> }>) {
  const { contactId } = await params;
  const id = Number(contactId);
  if (!Number.isInteger(id) || id <= 0) notFound();

  return <ContactDetail contactId={id} />;
}
