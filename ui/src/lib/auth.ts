import { useQuery } from "@tanstack/react-query";
import { getMeOptions, getMeQueryKey } from "@/api/@tanstack/react-query.gen";
import type { Me } from "@/api/types.gen";

export type { Me };

export const meQueryKey = getMeQueryKey();

/** useMe loads the current user. A 401 error means "not signed in". */
export function useMe() {
  return useQuery({ ...getMeOptions(), retry: false, staleTime: 60_000 });
}

/** useCurrentUser returns the signed-in user. Use it only inside the app shell. */
export function useCurrentUser(): Me {
  const { data } = useMe();
  if (!data) throw new Error("The current user is not loaded.");
  return data;
}
