/** TreeRow is one namespace with its depth in the tree. */
export type TreeRow<T> = { item: T; depth: number };

function compareSegments(a: string[], b: string[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const x = a[i] ?? "";
    const y = b[i] ?? "";
    if (x !== y) return x < y ? -1 : 1;
  }
  return a.length - b.length;
}

/**
 * namespaceTree orders namespaces depth first (each parent before its children)
 * and returns the depth of each one. The depth counts the ancestors in the list.
 */
export function namespaceTree<T extends { name: string }>(items: T[]): TreeRow<T>[] {
  const names = new Set(items.map((i) => i.name));
  return items
    .map((item) => ({ item, segs: item.name.split(".") }))
    .sort((a, b) => compareSegments(a.segs, b.segs))
    .map(({ item, segs }) => {
      let depth = 0;
      for (let i = 1; i < segs.length; i++) {
        if (names.has(segs.slice(0, i).join("."))) depth++;
      }
      return { item, depth };
    });
}
