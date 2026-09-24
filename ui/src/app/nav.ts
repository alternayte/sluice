import {
  Braces,
  FileClock,
  FolderTree,
  GitBranch,
  HardDrive,
  KeyRound,
  LayoutDashboard,
  LockKeyhole,
  Play,
  Server,
  Sparkles,
  UserRound,
  Users,
  Vault,
  Workflow,
  type LucideIcon,
} from "lucide-react";
import type { Role } from "@/lib/roles";

/** NavItem is one page of the side nav. chord is the second key of its "g" shortcut. */
export type NavItem = { to: string; label: string; min: Role; icon: LucideIcon; chord?: string };

export const navItems: NavItem[] = [
  { to: "/", label: "Dashboard", min: "viewer", icon: LayoutDashboard, chord: "d" },
  { to: "/executions", label: "Executions", min: "viewer", icon: Play, chord: "e" },
  { to: "/flows", label: "Flows", min: "viewer", icon: Workflow, chord: "f" },
  { to: "/namespaces", label: "Namespaces", min: "viewer", icon: FolderTree, chord: "n" },
  { to: "/secrets", label: "Secrets", min: "viewer", icon: LockKeyhole, chord: "s" },
  { to: "/variables", label: "Variables", min: "viewer", icon: Braces, chord: "v" },
];

export const settingsItems: NavItem[] = [
  { to: "/settings/profile", label: "Profile", min: "viewer", icon: UserRound, chord: "p" },
  { to: "/settings/tokens", label: "API tokens", min: "viewer", icon: KeyRound, chord: "t" },
  { to: "/settings/users", label: "Users", min: "admin", icon: Users, chord: "u" },
  { to: "/settings/git", label: "Git sources", min: "admin", icon: GitBranch, chord: "g" },
  { to: "/settings/secret-providers", label: "Secret providers", min: "admin", icon: Vault },
  { to: "/settings/storage", label: "Storage", min: "admin", icon: HardDrive },
  { to: "/settings/ai", label: "AI provider", min: "admin", icon: Sparkles },
  { to: "/settings/instances", label: "Instances", min: "admin", icon: Server, chord: "i" },
  { to: "/settings/audit", label: "Audit log", min: "admin", icon: FileClock, chord: "a" },
];
