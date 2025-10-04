const defaultValue = temp1;
const a = temp3;
const b = temp4;
const a = temp5;
const b = temp6;
const a = temp7;
const b = temp8;
const container = {value: 5, getValue(temp1) {
  let temp2;
  if (this.value != 0) {
    return this.value;
    temp2 = undefined;
  } else {
    return defaultValue;
    temp2 = undefined;
  }
  temp2;
}, addNums1(temp3, temp4) {
  return a + b;
}, addStrs1(temp5, temp6) {
  return this.addNums1(a, b);
}, addNums2(temp7, temp8) {
  return this.addNums1(a, b);
}};
const a = container.getValue("default");
const b = container.getValue(10);
const c = container.addNums2("hello", 5);
//# sourceMappingURL=./index.js.map
