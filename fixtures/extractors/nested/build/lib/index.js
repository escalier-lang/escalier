class D {
  constructor(temp2) {
    const msg = temp2;
    this.msg = msg;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.msg];
  }
}
class E {
  constructor(temp4, temp5) {
    const x = temp4;
    const y = temp5;
    this.x = x;
    this.y = y;
  }
  static [Symbol.customMatcher](temp3) {
    const subject = temp3;
    return [subject.x, subject.y];
  }
}
class C {
  constructor(temp7, temp8) {
    const d = temp7;
    const e = temp8;
    this.d = d;
    this.e = e;
  }
  static [Symbol.customMatcher](temp6) {
    const subject = temp6;
    return [subject.d, subject.e];
  }
}
const subject = new C(new D("hello"), new E(5, 10));
const [temp9, temp10] = InvokeCustomMatcherOrThrow(C, subject, undefined);
const [temp11] = InvokeCustomMatcherOrThrow(D, temp9, undefined);
const msg = temp11;
const [temp12, temp13] = InvokeCustomMatcherOrThrow(E, temp10, undefined);
const x = temp12;
const y = temp13;
//# sourceMappingURL=./index.js.map
