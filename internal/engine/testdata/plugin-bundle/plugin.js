var __create = Object.create;
var __defProp = Object.defineProperty;
var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
var __getOwnPropNames = Object.getOwnPropertyNames;
var __getProtoOf = Object.getPrototypeOf;
var __hasOwnProp = Object.prototype.hasOwnProperty;
var __commonJS = (cb, mod) => function __require() {
  return mod || (0, cb[__getOwnPropNames(cb)[0]])((mod = { exports: {} }).exports, mod), mod.exports;
};
var __copyProps = (to, from, except, desc) => {
  if (from && typeof from === "object" || typeof from === "function") {
    for (let key of __getOwnPropNames(from))
      if (!__hasOwnProp.call(to, key) && key !== except)
        __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
  }
  return to;
};
var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(
  // If the importer is in node compatibility mode or this is not an ESM
  // file that has been converted to a CommonJS file using a Babel-
  // compatible transform (i.e. "__esModule" has not been set), then set
  // "default" to the CommonJS "module.exports" for node compatibility.
  isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", { value: mod, enumerable: true }) : target,
  mod
));

// testdata/plugin-bundle/src/legacy.cjs
var require_legacy = __commonJS({
  "testdata/plugin-bundle/src/legacy.cjs"(exports, module) {
    var pad = (value, width) => String(value).padStart(width, "0");
    exports.stamp = function stamp2(date) {
      return `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1, 2)}-${pad(date.getUTCDate(), 2)}`;
    };
    module.exports.version = "2";
  }
});

// testdata/plugin-bundle/src/signer.ts
var Credential = class _Credential {
  static #count = 0;
  #secret;
  constructor(secret) {
    this.#secret = secret;
    _Credential.#count++;
  }
  static get count() {
    return _Credential.#count;
  }
  get secret() {
    return this.#secret;
  }
};
var BasicCredential = class extends Credential {
  constructor(user, password) {
    super(password);
    this.user = user;
  }
  header() {
    return `${"Basic" /* Basic */} ${btoa(`${this.user}:${this.secret}`)}`;
  }
};
var BearerCredential = class extends Credential {
  constructor(token) {
    super(token);
  }
  header() {
    return `${"Bearer" /* Bearer */} ${this.secret}`;
  }
};
function credential(values) {
  return values.token ? new BearerCredential(values.token) : new BasicCredential(values.user ?? "", values.password ?? "");
}

// testdata/plugin-bundle/src/format.ts
var import_legacy = __toESM(require_legacy());
function* pairs(items) {
  let index = 0;
  for (const item of items) yield [index++, item];
}
async function* slowly(items) {
  for (const item of items) {
    await new Promise((done) => setTimeout(done, 1));
    yield item;
  }
}
function summarize(rows) {
  const tags = new Set(rows.flatMap((row) => row.tags ?? []));
  const sizes = new Map(rows.map(({ name, size = 0 }) => [name, size]));
  const total = [...sizes.values()].reduce((sum, size) => sum + size, 0);
  const counts = Object.fromEntries(Object.entries(Object.groupBy(rows, (row) => (row.size ?? 0) > 10 ? "big" : "small")).map(([key, list]) => [key, list?.length ?? 0]));
  return JSON.stringify({ tags: [...tags].sort(), total, counts, big: 2n ** 64n + "", day: (0, import_legacy.stamp)(new Date(Date.UTC(2026, 10, 8))), version: import_legacy.version });
}
function parseLink(header) {
  const links = {};
  for (const match of header.matchAll(/<(?<url>[^>]+)>;\s*rel="(?<rel>\w+)"/g)) {
    const { url, rel } = match.groups;
    links[rel] = new URL(url).searchParams.get("page") ?? "";
  }
  return links;
}

// testdata/plugin-bundle/src/index.ts
var index_default = {
  name: "Bundled",
  version: "1.0.0",
  templates: [
    {
      name: "bundled.summary",
      run(_ctx, args) {
        return summarize(JSON.parse(args.rows));
      }
    },
    {
      name: "bundled.links",
      run(_ctx, args) {
        return JSON.stringify(parseLink(args.header));
      }
    },
    {
      name: "bundled.stream",
      async run() {
        const seen = [];
        for await (const word of slowly(["a", "b", "c"])) seen.push(word);
        for (const [index, word] of pairs(seen)) seen[index] = word.toUpperCase();
        const copy = structuredClone({ seen, when: /* @__PURE__ */ new Date(0) });
        return `${copy.seen.join("")}:${copy.when.getTime()}:${Credential.count}`;
      }
    }
  ],
  authentication: [
    {
      name: "bundled.auth",
      label: "Bundled",
      fields: [{ name: "user" }, { name: "password", secret: true }, { name: "token", secret: true }],
      async apply(_ctx, values, request) {
        request.headers.Authorization = credential(values).header();
        const text = typeof request.body === "string" ? request.body : new TextDecoder().decode(request.body);
        request.body = new TextEncoder().encode(text.replace(/\{\{(\w+)\}\}/g, (_, key) => values[key] ?? ""));
      }
    }
  ]
};
export {
  index_default as default
};
