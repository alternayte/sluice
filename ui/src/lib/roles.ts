export const roles = ["viewer", "operator", "editor", "admin"] as const;

export type Role = (typeof roles)[number];

export const roleLabels: Record<Role, string> = {
  viewer: "Viewer",
  operator: "Operator",
  editor: "Editor",
  admin: "Admin",
};

export function isRole(v: unknown): v is Role {
  return typeof v === "string" && (roles as readonly string[]).includes(v);
}

/** can reports whether role is at least min. An unknown role can do nothing. */
export function can(role: string | null | undefined, min: Role): boolean {
  if (!isRole(role)) return false;
  return roles.indexOf(role) >= roles.indexOf(min);
}

/** rolesUpTo returns the roles that are not higher than role. */
export function rolesUpTo(role: string | null | undefined): Role[] {
  return roles.filter((r) => can(role, r));
}
