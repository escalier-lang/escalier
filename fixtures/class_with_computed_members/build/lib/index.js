export const bar = "bar";
export const baz = "baz";
export class Foo {
  [baz]() {
    return this[bar];
  }
  constructor(temp1) {
    const barVal = temp1;
    let temp2;
    if (barVal != undefined) {
      temp2 = barVal;
    } else {
      temp2 = 42;
    }
    this[bar] = temp2;
  }
}
export const foo = new Foo();
export const fooBar = foo[bar];
export const fooBaz = foo[baz]();
//# sourceMappingURL=./index.js.map
