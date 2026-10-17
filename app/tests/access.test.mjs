import assert from "node:assert/strict";
import test from "node:test";
import { authorized, accessDenied, withAccess } from "../src/lib/access.ts";

const policy = { password: "web-secret", trustedLAN: false };
const basic = (value) => new Headers({ authorization: `Basic ${Buffer.from(value).toString("base64")}` });

test("anonymous requests fail closed regardless of claimed local or proxy identity", () => {
  for (const headers of [new Headers(), new Headers({ host: "localhost", "x-forwarded-for": "127.0.0.1", "x-middleware-subrequest": "proxy" })]) {
    assert.equal(authorized(headers, policy), false);
    assert.equal(accessDenied(headers, policy).status, 401);
  }
  assert.equal(accessDenied(new Headers(), { password: "", trustedLAN: false }).status, 503);
});

test("web credentials are independent and require the configured identity", () => {
  assert.equal(authorized(basic("castor:web-secret"), policy), true);
  for (const value of ["other:web-secret", "castor:api-token", "castor:", "castor:web-secret:extra"]) {
    assert.equal(authorized(basic(value), policy), false);
  }
  assert.equal(authorized(new Headers({ authorization: "Bearer web-secret" }), policy), false);
  assert.equal(accessDenied(basic("castor:web-secret"), policy), undefined);
});

test("trusted LAN is explicit and does not bypass a configured password", () => {
  assert.equal(authorized(new Headers(), { password: "", trustedLAN: true }), true);
  assert.equal(authorized(new Headers(), { ...policy, trustedLAN: true }), false);
});

test("unauthorized responses challenge browsers and cannot be cached", () => {
  const response = accessDenied(new Headers(), policy);
  assert.match(response.headers.get("www-authenticate"), /^Basic /);
  assert.equal(response.headers.get("cache-control"), "no-store");
});


test("watch requests cannot reach the upstream callback before authorization", async () => {
  let calls = 0;
  const upstream = async () => { calls++; return new Response("stream"); };
  const req = new Request("https://castor.test/rpc/api/castor.v1.CastService/Watch", { method: "POST" });
  assert.equal((await withAccess(req.headers, upstream, policy)).status, 401);
  assert.equal(calls, 0);
  assert.equal(await (await withAccess(basic("castor:web-secret"), upstream, policy)).text(), "stream");
  assert.equal(calls, 1);
});
