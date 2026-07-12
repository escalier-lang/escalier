import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class C {
  constructor(temp1, temp2) {
    const d = temp1;
    const e = temp2;
    this.d = d;
    this.e = e;
  }
  static [Symbol.customMatcher](temp3) {
    const subject = temp3;
    return [subject.d, subject.e];
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
export class E {
  constructor(temp6, temp7) {
    const x = temp6;
    const y = temp7;
    this.x = x;
    this.y = y;
  }
  static [Symbol.customMatcher](temp8) {
    const subject = temp8;
    return [subject.x, subject.y];
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
