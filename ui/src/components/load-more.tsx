import { Button } from "@/components/ui/button";

/** LoadMore shows a "Load more" button for an infinite query with a next page. */
export function LoadMore({
  query,
}: {
  query: { hasNextPage: boolean; isFetchingNextPage: boolean; fetchNextPage: () => unknown };
}) {
  if (!query.hasNextPage) return null;
  return (
    <div className="flex justify-center pt-3">
      <Button variant="secondary" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
        {query.isFetchingNextPage ? "Loading" : "Load more"}
      </Button>
    </div>
  );
}
