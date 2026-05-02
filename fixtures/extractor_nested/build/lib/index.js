import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class E {
  constructor(temp1, temp2) {
    const x = temp1;
    const y = temp2;
    this.x = x;
    this.y = y;
  }
  static [Symbol.customMatcher](temp3) {
    const subject = temp3;
    return [subject.x, subject.y];
  }
}
export class D {
  constructor(temp4) {
    const msg = temp4;
    this.msg = msg;
  }
  static [Symbol.customMatcher](temp5) {
    const subject = temp5;
    return [subject.msg];
  }
}
export const [temp6, temp7] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const [temp8] = InvokeCustomMatcherOrThrow(D, temp6, undefined);
export const msg = temp8;
export const [temp9, temp10] = InvokeCustomMatcherOrThrow(E, temp7, undefined);
export const x = temp9;
export const y = temp10;
export class C {
  constructor(temp11, temp12) {
    const d = temp11;
    const e = temp12;
    this.d = d;
    this.e = e;
  }
  static [Symbol.customMatcher](temp13) {
    const subject = temp13;
    return [subject.d, subject.e];
  }
}
export const subject = new C(new D("hello"), new E(5, 10));
//# sourceMappingURL=./index.js.map
