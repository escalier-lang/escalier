const foo = "foo";
const bar = "bar";
const obj = {[foo]: 42, [bar]() {
  return this[foo];
}};
const a = obj[foo];
const b = obj[bar]();
//# sourceMappingURL=./index.js.map
