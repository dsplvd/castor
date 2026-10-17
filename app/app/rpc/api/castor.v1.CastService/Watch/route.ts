import { withAccess } from "@/lib/access";
import { env } from "@/lib/env";
import { proxyWatch } from "@/lib/watch-proxy";

// Watch is the only call a browser makes; everything else runs in server actions.
export async function POST(req: Request) {
  return withAccess(req.headers, () => proxyWatch(req, env.api));
}
