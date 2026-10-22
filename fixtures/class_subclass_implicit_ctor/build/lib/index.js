export class Base {
  constructor(temp1) {
    const foo = temp1;
    this.foo = foo;
  }
}
export class Derived2 extends Base {
  constructor(temp2, temp3) {
    const foo = temp2;
    const bar = temp3;
    super(foo);
    this.bar = bar;
  }
}
export class Derived1 extends Base {
  constructor(temp4, temp5) {
    const foo = temp4;
    const bar = temp5;
    super(foo);
    this.bar = bar;
  }
}
export class Leaf extends Derived2 {
  constructor(temp6, temp7, temp8) {
    const foo = temp6;
    const bar = temp7;
    const baz = temp8;
    super(foo, bar);
    this.baz = baz;
  }
}
export const d1 = new Derived1("a", 1);
export const d2 = new Derived2("b", 2);
export const leaf = new Leaf("c", 3, true);
//# sourceMappingURL=./index.js.map
