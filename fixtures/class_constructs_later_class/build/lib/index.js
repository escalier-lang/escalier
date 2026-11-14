export class B {
  constructor(temp1) {
    const n = temp1;
    this.n = n;
  }
  toA() {
    return new A(this.n);
  }
}
export class A {
  constructor(temp2) {
    const v = temp2;
    this.v = v;
  }
  static mk() {
    return new B(1);
  }
}
//# sourceMappingURL=./index.js.map
