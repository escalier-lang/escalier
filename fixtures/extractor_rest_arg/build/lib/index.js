import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class C {
  constructor(temp1, temp2, temp3) {
    const msg = temp1;
    const value = temp2;
    const flag = temp3;
    this.msg = msg;
    this.value = value;
    this.flag = flag;
  }
  static [Symbol.customMatcher](temp4) {
    const subject = temp4;
    return [subject.msg, subject.value, subject.flag];
  }
}
export const subject = new C("hello", 5, true);
export const [temp5, ...temp6] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const x = temp5;
export const y = temp6;
//# sourceMappingURL=./index.js.map
