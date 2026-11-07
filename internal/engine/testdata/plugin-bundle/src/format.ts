import { stamp, version } from "./legacy.cjs";

export type Row = { name: string; tags?: string[]; size?: number };

export function* pairs<T>(items: Iterable<T>): Generator<[number, T]> {
  let index = 0;
  for (const item of items) yield [index++, item];
}

export async function* slowly<T>(items: T[]): AsyncGenerator<T> {
  for (const item of items) {
    await new Promise((done) => setTimeout(done, 1));
    yield item;
  }
}

export function summarize(rows: Row[]): string {
  const tags = new Set(rows.flatMap((row) => row.tags ?? []));
  const sizes = new Map(rows.map(({ name, size = 0 }) => [name, size] as const));
  const total = [...sizes.values()].reduce((sum, size) => sum + size, 0);
  const counts = Object.fromEntries(Object.entries(Object.groupBy(rows, (row) => (row.size ?? 0) > 10 ? "big" : "small")).map(([key, list]) => [key, list?.length ?? 0]));
  return JSON.stringify({ tags: [...tags].sort(), total, counts, big: 2n ** 64n + "", day: stamp(new Date(Date.UTC(2026, 10, 8))), version });
}

export function parseLink(header: string): Record<string, string> {
  const links: Record<string, string> = {};
  for (const match of header.matchAll(/<(?<url>[^>]+)>;\s*rel="(?<rel>\w+)"/g)) {
    const { url, rel } = match.groups!;
    links[rel] = new URL(url).searchParams.get("page") ?? "";
  }
  return links;
}
