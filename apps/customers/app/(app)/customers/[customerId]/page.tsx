import { notFound } from "next/navigation";
import { CustomerDetail } from "./customer-detail";

export default async function Page({
  params,
}: Readonly<{ params: Promise<{ customerId: string }> }>) {
  const { customerId } = await params;
  const id = Number(customerId);
  if (!Number.isInteger(id) || id <= 0) notFound();

  return <CustomerDetail customerId={id} />;
}
