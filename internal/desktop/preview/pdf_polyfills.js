
function getOrInsert(key, value) {
  if (this.has(key)) return this.get(key);
  this.set(key, value);
  return value;
}

function getOrInsertComputed(key, callbackfn) {
  if (typeof callbackfn !== "function") {
    throw new TypeError("getOrInsertComputed callback is not a function");
  }
  if (this.has(key)) return this.get(key);
  const k = Object.is(key, -0) ? 0 : key;
  const value = callbackfn(k);
  this.set(k, value);
  return value;
}

function sumPrecise(items) {
  const partials = [];
  let allNegativeZero = true;
  let sawNaN = false;
  let sawPositiveInfinity = false;
  let sawNegativeInfinity = false;

  for (const item of items) {
    if (typeof item !== "number") {
      throw new TypeError("Math.sumPrecise requires an iterable of numbers");
    }
    if (!Object.is(item, -0)) allNegativeZero = false;
    if (Number.isNaN(item)) {
      sawNaN = true;
    } else if (item === Infinity) {
      sawPositiveInfinity = true;
    } else if (item === -Infinity) {
      sawNegativeInfinity = true;
    } else {
      let x = item;
      let i = 0;
      for (let y of partials) {
        if (Math.abs(x) < Math.abs(y)) [x, y] = [y, x];
        const hi = x + y;
        const lo = y - (hi - x);
        if (lo !== 0) partials[i++] = lo;
        x = hi;
      }
      partials.length = i;
      partials.push(x);
    }
  }

  if (sawNaN || (sawPositiveInfinity && sawNegativeInfinity)) return NaN;
  if (sawPositiveInfinity) return Infinity;
  if (sawNegativeInfinity) return -Infinity;
  if (allNegativeZero) return -0;

  let n = partials.length;
  let hi = 0;
  let lo = 0;
  if (n > 0) {
    hi = partials[--n];
    while (n > 0) {
      const x = hi;
      const y = partials[--n];
      hi = x + y;
      lo = y - (hi - x);
      if (lo !== 0) break;
    }
    const next = partials[n - 1];
    if (n > 0 && next != null && ((lo < 0 && next < 0) || (lo > 0 && next > 0))) {
      const y = lo * 2;
      const x = hi + y;
      if (y === x - hi) hi = x;
    }
  }
  return hi === 0 ? 0 : hi;
}

async function* readableStreamValues({ preventCancel = false } = {}) {
  const reader = this.getReader();
  let finished = false;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        finished = true;
        return;
      }
      yield value;
    }
  } catch (err) {
    finished = true;
    throw err;
  } finally {
    try {
      if (!finished && !preventCancel) await reader.cancel();
    } finally {
      reader.releaseLock();
    }
  }
}

function define(target, key, value) {
  if (key in target) return;
  Object.defineProperty(target, key, { value, writable: true, configurable: true });
}

define(Map.prototype, "getOrInsert", getOrInsert);
define(Map.prototype, "getOrInsertComputed", getOrInsertComputed);
define(WeakMap.prototype, "getOrInsert", getOrInsert);
define(WeakMap.prototype, "getOrInsertComputed", getOrInsertComputed);
define(Math, "sumPrecise", sumPrecise);
if (typeof ReadableStream !== "undefined") {
  define(ReadableStream.prototype, "values", readableStreamValues);
  define(ReadableStream.prototype, Symbol.asyncIterator, Reflect.get(ReadableStream.prototype, "values"));
}
