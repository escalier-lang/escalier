export const bar = "bar";
export const baz = "baz";
export class Foo {
  [baz]() {
    return this[bar];
  }
  constructor() {
  }
}
export const foo = new Foo();
export const fooBar = foo[bar];
export const fooBaz = foo[baz]();
//# sourceMappingURL=./index.js.map
