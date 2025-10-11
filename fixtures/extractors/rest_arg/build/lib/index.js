class C {
  constructor(temp2, temp3, temp4) {
    const msg = temp2;
    const value = temp3;
    const flag = temp4;
    this.msg = msg;
    this.value = value;
    this.flag = flag;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.msg, subject.value, subject.flag];
  }
}
const subject = new C("hello", 5, true);
const [temp5, temp6] = InvokeCustomMatcherOrThrow(C, subject, undefined);
const x = temp5;
const ...y = temp6;
//# sourceMappingURL=./index.js.map
