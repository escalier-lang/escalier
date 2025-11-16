import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class D {
  constructor(temp2) {
    const msg = temp2;
    this.msg = msg;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.msg];
  }
}
export class E {
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
export class C {
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
export const subject = new C(new D("hello"), new E(5, 10));
export const [temp9, temp10] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const [temp11] = InvokeCustomMatcherOrThrow(D, temp9, undefined);
export const msg = temp11;
export const [temp12, temp13] = InvokeCustomMatcherOrThrow(E, temp10, undefined);
export const x = temp12;
export const y = temp13;
//# sourceMappingURL=./index.js.map
