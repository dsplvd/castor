import "server-only";
import { devices } from "@/lib/server";

// Authorization reads this request's headers at the RPC boundary. Never put
// that boundary inside Next's cross-request cache or reuse another visitor's
// authorized result.
export async function listDevices() {
  return (await devices.listDevices({})).devices;
}
