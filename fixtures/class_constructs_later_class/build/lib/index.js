export class A {
  constructor(temp1) {
    const v = temp1;
    this.v = v;
  }
  static mk() {
    return new B(1);
  }
}
export class B {
  constructor(temp2) {
    const n = temp2;
    this.n = n;
  }
  toA() {
    return new A(this.n);
  }
}
//# sourceMappingURL=./index.js.map
