"use server";

import { authorize } from "@/lib/authorize";

import { revalidatePath } from "next/cache";

export async function refreshDevicesAction() {
  await authorize();
  revalidatePath("/");
}
