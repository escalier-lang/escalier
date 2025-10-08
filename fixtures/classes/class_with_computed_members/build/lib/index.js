const bar = "bar";
const baz = "baz";
class Foo {
  constructor() {
    this[bar] = 42;
  }
  [baz]() {
    return this[bar];
  }
}
const foo = new Foo();
const fooBar = foo[bar];
const fooBaz = foo[baz]();
//# sourceMappingURL=./index.js.map
