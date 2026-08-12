export class A {
  constructor(temp1) {
    const x = temp1;
    this.x = x;
  }
}
export class B extends A {
  constructor() {
    super();
  }
}
export class C extends B {
  constructor() {
    super();
  }
}
export const c = new C();
export const x = c.x;
//# sourceMappingURL=./index.js.map
