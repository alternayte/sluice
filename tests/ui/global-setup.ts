import { execFileSync, spawn } from "node:child_process";
import { openSync, writeFileSync } from "node:fs";
import net from "node:net";
import path from "node:path";
import { adminEmail, adminPassword, stateFile, uiTestsDir } from "./helpers/constants";

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      if (addr && typeof addr === "object") {
        const port = addr.port;
        srv.close(() => resolve(port));
      } else reject(new Error("no port"));
    });
  });
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

/** Starts Postgres in Docker and the sluice binary, then waits for /readyz. */
export default async function globalSetup() {
  const container = execFileSync("docker", [
    "run", "-d", "--rm", "-p", "127.0.0.1::5432",
    "-e", "POSTGRES_PASSWORD=sluice", "-e", "POSTGRES_USER=sluice", "-e", "POSTGRES_DB=sluice",
    "postgres:17-alpine",
  ]).toString().trim();
  // A busy Docker engine can report the mapped port a little later than the start.
  let pgPort = "";
  for (let i = 0; i < 60 && !/^\d+$/.test(pgPort); i++) {
    try {
      const mapped = execFileSync("docker", ["port", container, "5432/tcp"]).toString().trim().split("\n")[0] ?? "";
      pgPort = mapped.split(":").pop() ?? "";
    } catch {
      pgPort = "";
    }
    if (!/^\d+$/.test(pgPort)) await sleep(500);
  }
  if (!/^\d+$/.test(pgPort)) throw new Error(`no mapped port for the Postgres container ${container}`);
  for (let i = 0; i < 120; i++) {
    try {
      execFileSync("docker", ["exec", container, "pg_isready", "-U", "sluice", "-h", "127.0.0.1"], { stdio: "ignore" });
      break;
    } catch {
      await sleep(500);
    }
  }
  await sleep(1000);

  const port = await freePort();
  const baseURL = `http://127.0.0.1:${port}`;
  const binary = process.env.SLUICE_E2E_BINARY ?? path.resolve(uiTestsDir, "../../bin/sluice");
  const logFile = path.resolve(uiTestsDir, "../../build/reports/playwright-server.log");
  execFileSync("mkdir", ["-p", path.dirname(logFile)]);
  const out = openSync(logFile, "w");
  const server = spawn(binary, ["server"], {
    env: {
      PATH: process.env.PATH ?? "",
      HOME: process.env.HOME ?? "",
      SLUICE_DATABASE_URL: `postgres://sluice:sluice@127.0.0.1:${pgPort}/sluice?sslmode=disable`,
      SLUICE_LISTEN_ADDR: `127.0.0.1:${port}`,
      SLUICE_PUBLIC_URL: baseURL,
      SLUICE_BOOTSTRAP_ADMIN_EMAIL: adminEmail,
      SLUICE_BOOTSTRAP_ADMIN_PASSWORD: adminPassword,
      SLUICE_EXECUTORS: "process",
      // A fixed test master key (32 bytes of 0x01) for builtin secrets.
      SLUICE_MASTER_KEYS: `k1:${Buffer.alloc(32, 1).toString("base64")}`,
      SLUICE_LOG_FORMAT: "text",
      // A value for the env secret provider check of SCN-UI-006.
      SLUICE_SECRET_UI_CHECK: "ui-check-value",
    },
    stdio: ["ignore", out, out],
    detached: true,
  });
  server.unref();

  let ready = false;
  for (let i = 0; i < 240 && !ready; i++) {
    try {
      const res = await fetch(`${baseURL}/readyz`);
      ready = res.status === 200;
    } catch {
      // The server is starting.
    }
    if (!ready) await sleep(250);
  }
  if (!ready) throw new Error(`sluice server did not become ready, see ${logFile}`);

  process.env.SLUICE_UI_BASE_URL = baseURL;
  process.env.SLUICE_UI_DATABASE_URL = `postgres://sluice:sluice@127.0.0.1:${pgPort}/sluice?sslmode=disable`;
  writeFileSync(stateFile, JSON.stringify({ container, pid: server.pid, baseURL }));
}
