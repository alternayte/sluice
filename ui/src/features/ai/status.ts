import { useQuery } from "@tanstack/react-query";
import { getAiStatusOptions } from "@/api/@tanstack/react-query.gen";

/** useAiStatus tells whether an AI provider is configured. Without one the UI hides AI actions (REQ-AI-002). */
export function useAiStatus() {
  return useQuery({ ...getAiStatusOptions(), staleTime: 30_000 });
}

/** useAiEnabled is true when an AI provider is configured. */
export function useAiEnabled(): boolean {
  return useAiStatus().data?.enabled === true;
}
