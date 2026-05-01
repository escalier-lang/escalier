export class Foo {
  constructor(temp1) {
    const value = temp1;
    let temp2;
    if (value != undefined) {
      temp2 = value;
    } else {
      temp2 = 5;
    }
    this.value = temp2;
  }
  getValue(temp3) {
    const defaultValue = temp3;
    if (this.value != 0) {
      return this.value;
    } else {
      return defaultValue;
    }
  }
  static addNums1(temp4, temp5) {
    const a = temp4;
    const b = temp5;
    return a + b;
  }
  addStrs1(temp6, temp7) {
    const a = temp6;
    const b = temp7;
    return Foo.addNums1(a, b);
  }
  addNums2(temp8, temp9) {
    const a = temp8;
    const b = temp9;
    return Foo.addNums1(a, b);
  }
  static addStrs3(temp10, temp11) {
    const a = temp10;
    const b = temp11;
    return Foo.addNums1(a, b);
  }
  static addNums4(temp12, temp13) {
    const a = temp12;
    const b = temp13;
    return Foo.addNums1(a, b);
  }
}
export const foo = new Foo();
export const a = foo.getValue("default");
export const b = foo.getValue(10);
export const c = foo.addNums2("hello", 5);
export const d = Foo.addNums4("hello", 5);
//# sourceMappingURL=./index.js.map
