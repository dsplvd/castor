import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";
import { accessDenied } from "./src/lib/access";

export function proxy(request: NextRequest) {
  return accessDenied(request.headers) ?? NextResponse.next();
}
