import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class C {
  constructor(temp2, temp3) {
    const d = temp2;
    const e = temp3;
    this.d = d;
    this.e = e;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.d, subject.e];
  }
}
export class D {
  constructor(temp5) {
    const msg = temp5;
    this.msg = msg;
  }
  static [Symbol.customMatcher](temp4) {
    const subject = temp4;
    return [subject.msg];
  }
}
export class E {
  constructor(temp7, temp8) {
    const x = temp7;
    const y = temp8;
    this.x = x;
    this.y = y;
  }
  static [Symbol.customMatcher](temp6) {
    const subject = temp6;
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
export const [temp14, temp15] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const [temp16] = InvokeCustomMatcherOrThrow(D, temp14, undefined);
export const msg = temp16;
export const [temp17, temp18] = InvokeCustomMatcherOrThrow(E, temp15, undefined);
export const x = temp17;
export const y = temp18;
export const [temp19, temp20] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const [temp21] = InvokeCustomMatcherOrThrow(D, temp19, undefined);
export const msg = temp21;
export const [temp22, temp23] = InvokeCustomMatcherOrThrow(E, temp20, undefined);
export const x = temp22;
export const y = temp23;
//# sourceMappingURL=./index.js.map
