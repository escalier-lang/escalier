class Foo {
  constructor() {
    this.value = 5;
  }
  getValue(temp1) {
    const defaultValue = temp1;
    let temp2;
    if (this.value != 0) {
      return this.value;
      temp2 = undefined;
    } else {
      return defaultValue;
      temp2 = undefined;
    }
    temp2;
  }
  static addNums1(temp3, temp4) {
    const a = temp3;
    const b = temp4;
    return a + b;
  }
  addStrs1(temp5, temp6) {
    const a = temp5;
    const b = temp6;
    return Foo.addNums1(a, b);
  }
  addNums2(temp7, temp8) {
    const a = temp7;
    const b = temp8;
    return Foo.addNums1(a, b);
  }
  static addStrs3(temp9, temp10) {
    const a = temp9;
    const b = temp10;
    return Foo.addNums1(a, b);
  }
  static addNums4(temp11, temp12) {
    const a = temp11;
    const b = temp12;
    return Foo.addNums1(a, b);
  }
}
const foo = Foo();
const a = foo.getValue("default");
const b = foo.getValue(10);
const c = foo.addNums2("hello", 5);
const d = Foo.addNums4("hello", 5);
//# sourceMappingURL=./index.js.map
