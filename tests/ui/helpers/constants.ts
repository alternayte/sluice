import path from "node:path";
import { fileURLToPath } from "node:url";

export const adminEmail = "admin@example.com";
export const adminPassword = "admin-password-1";
export const uiTestsDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const stateFile = path.join(uiTestsDir, ".server-state.json");
