export const bar = "bar";
export const baz = "baz";
export class Foo {
  constructor() {
    this[bar] = 42;
  }
  [baz]() {
    return this[bar];
  }
}
export const foo = new Foo();
export const fooBar = foo[bar];
export const fooBaz = foo[baz]();
//# sourceMappingURL=./index.js.map
