import { credential, Credential } from "./signer";
import { pairs, parseLink, slowly, summarize, type Row } from "./format";

type Context = { getValue(key: string): string; setValue(key: string, value: string): void };
type Request = { headers: Record<string, string | string[]>; body: Uint8Array | string };

export default {
  name: "Bundled",
  version: "1.0.0",
  templates: [
    {
      name: "bundled.summary",
      run(_ctx: Context, args: Record<string, string>): string {
        return summarize(JSON.parse(args.rows) as Row[]);
      },
    },
    {
      name: "bundled.links",
      run(_ctx: Context, args: Record<string, string>): string {
        return JSON.stringify(parseLink(args.header));
      },
    },
    {
      name: "bundled.stream",
      async run(): Promise<string> {
        const seen: string[] = [];
        for await (const word of slowly(["a", "b", "c"])) seen.push(word);
        for (const [index, word] of pairs(seen)) seen[index] = word.toUpperCase();
        const copy = structuredClone({ seen, when: new Date(0) });
        return `${copy.seen.join("")}:${copy.when.getTime()}:${Credential.count}`;
      },
    },
  ],
  authentication: [
    {
      name: "bundled.auth",
      label: "Bundled",
      fields: [{ name: "user" }, { name: "password", secret: true }, { name: "token", secret: true }],
      async apply(_ctx: Context, values: Record<string, string>, request: Request): Promise<void> {
        request.headers.Authorization = credential(values).header();
        const text = typeof request.body === "string" ? request.body : new TextDecoder().decode(request.body);
        request.body = new TextEncoder().encode(text.replace(/\{\{(\w+)\}\}/g, (_, key: string) => values[key] ?? ""));
      },
    },
  ],
};
