var __create = Object.create;
var __defProp = Object.defineProperty;
var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
var __getOwnPropNames = Object.getOwnPropertyNames;
var __getProtoOf = Object.getPrototypeOf;
var __hasOwnProp = Object.prototype.hasOwnProperty;
var __knownSymbol = (name, symbol) => (symbol = Symbol[name]) ? symbol : Symbol.for("Symbol." + name);
var __typeError = (msg) => {
  throw TypeError(msg);
};
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
var __accessCheck = (obj, member, msg) => member.has(obj) || __typeError("Cannot " + msg);
var __privateGet = (obj, member, getter) => (__accessCheck(obj, member, "read from private field"), getter ? getter.call(obj) : member.get(obj));
var __privateAdd = (obj, member, value) => member.has(obj) ? __typeError("Cannot add the same private member more than once") : member instanceof WeakSet ? member.add(obj) : member.set(obj, value);
var __privateSet = (obj, member, value, setter) => (__accessCheck(obj, member, "write to private field"), setter ? setter.call(obj, value) : member.set(obj, value), value);
var __privateWrapper = (obj, member, setter, getter) => ({
  set _(value) {
    __privateSet(obj, member, value, setter);
  },
  get _() {
    return __privateGet(obj, member, getter);
  }
});
var __await = function(promise, isYieldStar) {
  this[0] = promise;
  this[1] = isYieldStar;
};
var __asyncGenerator = (__this, __arguments, generator) => {
  var resume = (k, v, yes, no) => {
    try {
      var x = generator[k](v), isAwait = (v = x.value) instanceof __await, done = x.done;
      Promise.resolve(isAwait ? v[0] : v).then((y) => isAwait ? resume(k === "return" ? k : "next", v[1] ? { done: y.done, value: y.value } : y, yes, no) : yes({ value: y, done })).catch((e) => resume("throw", e, yes, no));
    } catch (e) {
      no(e);
    }
  }, method = (k) => it[k] = (x) => new Promise((yes, no) => resume(k, x, yes, no)), it = {};
  return generator = generator.apply(__this, __arguments), it[__knownSymbol("asyncIterator")] = () => it, method("next"), method("throw"), method("return"), it;
};
var __forAwait = (obj, it, method) => (it = obj[__knownSymbol("asyncIterator")]) ? it.call(obj) : (obj = obj[__knownSymbol("iterator")](), it = {}, method = (key, fn) => (fn = obj[key]) && (it[key] = (arg) => new Promise((yes, no, done) => (arg = fn.call(obj, arg), done = arg.done, Promise.resolve(arg.value).then((value) => yes({ value, done }), no)))), method("next"), method("return"), it);

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
var _count, _secret;
var _Credential = class _Credential {
  constructor(secret) {
    __privateAdd(this, _secret);
    __privateSet(this, _secret, secret);
    __privateWrapper(_Credential, _count)._++;
  }
  static get count() {
    return __privateGet(_Credential, _count);
  }
  get secret() {
    return __privateGet(this, _secret);
  }
};
_count = new WeakMap();
_secret = new WeakMap();
__privateAdd(_Credential, _count, 0);
var Credential = _Credential;
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
  var _a, _b;
  return values.token ? new BearerCredential(values.token) : new BasicCredential((_a = values.user) != null ? _a : "", (_b = values.password) != null ? _b : "");
}

// testdata/plugin-bundle/src/format.ts
var import_legacy = __toESM(require_legacy());
function* pairs(items) {
  let index = 0;
  for (const item of items) yield [index++, item];
}
function slowly(items) {
  return __asyncGenerator(this, null, function* () {
    for (const item of items) {
      yield new __await(new Promise((done) => setTimeout(done, 1)));
      yield item;
    }
  });
}
function summarize(rows) {
  const tags = new Set(rows.flatMap((row) => {
    var _a;
    return (_a = row.tags) != null ? _a : [];
  }));
  const sizes = new Map(rows.map(({ name, size = 0 }) => [name, size]));
  const total = [...sizes.values()].reduce((sum, size) => sum + size, 0);
  const counts = Object.fromEntries(Object.entries(Object.groupBy(rows, (row) => {
    var _a;
    return ((_a = row.size) != null ? _a : 0) > 10 ? "big" : "small";
  })).map(([key, list]) => {
    var _a;
    return [key, (_a = list == null ? void 0 : list.length) != null ? _a : 0];
  }));
  return JSON.stringify({ tags: [...tags].sort(), total, counts, big: 2n ** 64n + "", day: (0, import_legacy.stamp)(new Date(Date.UTC(2026, 10, 8))), version: import_legacy.version });
}
function parseLink(header) {
  var _a;
  const links = {};
  for (const match of header.matchAll(new RegExp('<(?<url>[^>]+)>;\\s*rel="(?<rel>\\w+)"', "g"))) {
    const { url, rel } = match.groups;
    links[rel] = (_a = new URL(url).searchParams.get("page")) != null ? _a : "";
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
        try {
          for (var iter = __forAwait(slowly(["a", "b", "c"])), more, temp, error; more = !(temp = await iter.next()).done; more = false) {
            const word = temp.value;
            seen.push(word);
          }
        } catch (temp) {
          error = [temp];
        } finally {
          try {
            more && (temp = iter.return) && await temp.call(iter);
          } finally {
            if (error)
              throw error[0];
          }
        }
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
        request.body = new TextEncoder().encode(text.replace(/\{\{(\w+)\}\}/g, (_, key) => {
          var _a;
          return (_a = values[key]) != null ? _a : "";
        }));
      }
    }
  ]
};
export {
  index_default as default
};
