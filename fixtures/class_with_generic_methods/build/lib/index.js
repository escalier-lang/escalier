export class Foo {
  constructor() {
    this.value = 5;
  }
  getValue(temp1) {
    const defaultValue = temp1;
    if (this.value != 0) {
      return this.value;
    } else {
      return defaultValue;
    }
  }
  static addNums1(temp2, temp3) {
    const a = temp2;
    const b = temp3;
    return a + b;
  }
  addStrs1(temp4, temp5) {
    const a = temp4;
    const b = temp5;
    return Foo.addNums1(a, b);
  }
  addNums2(temp6, temp7) {
    const a = temp6;
    const b = temp7;
    return Foo.addNums1(a, b);
  }
  static addStrs3(temp8, temp9) {
    const a = temp8;
    const b = temp9;
    return Foo.addNums1(a, b);
  }
  static addNums4(temp10, temp11) {
    const a = temp10;
    const b = temp11;
    return Foo.addNums1(a, b);
  }
}
export const foo = new Foo();
export const a = foo.getValue("default");
export const b = foo.getValue(10);
export const c = foo.addNums2("hello", 5);
export const d = Foo.addNums4("hello", 5);
//# sourceMappingURL=./index.js.map
