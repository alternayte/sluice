import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import type { components } from "@/api/schema";

export type Me = components["schemas"]["Me"];

export const meQueryKey = ["auth", "me"] as const;

/** useMe loads the current user. A 401 error means "not signed in". */
export function useMe() {
  return useQuery({
    queryKey: meQueryKey,
    queryFn: async () => unwrap(await api.GET("/api/v1/auth/me")),
    retry: false,
    staleTime: 60_000,
  });
}

/** useCurrentUser returns the signed-in user. Use it only inside the app shell. */
export function useCurrentUser(): Me {
  const { data } = useMe();
  if (!data) throw new Error("The current user is not loaded.");
  return data;
}
