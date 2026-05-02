export const bar = "bar";
export const baz = "baz";
export class Foo {
  [baz]() {
    return this[bar];
  }
  constructor(temp1) {
    const barVal = typeof temp1 !== "undefined" ? temp1 : 42;
    this[bar] = barVal;
  }
}
export const foo = new Foo();
export const fooBar = foo[bar];
export const fooBaz = foo[baz]();
//# sourceMappingURL=./index.js.map
