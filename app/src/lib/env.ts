import "server-only";

const read = (key: string, fallback: string) => process.env[key] || fallback;

export const env = {
  api: { url: read("CASTOR_SERVERS__API__URL", "http://localhost:8411"), token: read("CASTOR_SERVERS__API__TOKEN", "") },
};
