export const foo = "foo";
export const bar = "bar";
export const obj = {[foo]: 42, [bar]() {
  return this[foo];
}};
export const a = obj[foo];
export const b = obj[bar]();
//# sourceMappingURL=./index.js.map
