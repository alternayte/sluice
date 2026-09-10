import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, rmSync } from "node:fs";
import { stateFile } from "./helpers/constants";

/** Stops the sluice server and removes the Postgres container. */
export default async function globalTeardown() {
  if (!existsSync(stateFile)) return;
  const state = JSON.parse(readFileSync(stateFile, "utf8")) as { container: string; pid: number };
  try {
    process.kill(state.pid, "SIGTERM");
  } catch {
    // The server already stopped.
  }
  try {
    execFileSync("docker", ["rm", "-f", state.container], { stdio: "ignore" });
  } catch {
    // The container already stopped.
  }
  rmSync(stateFile, { force: true });
}
