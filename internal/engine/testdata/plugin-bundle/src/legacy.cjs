const pad = (value, width) => String(value).padStart(width, "0");
exports.stamp = function stamp(date) {
  return `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1, 2)}-${pad(date.getUTCDate(), 2)}`;
};
module.exports.version = "2";
