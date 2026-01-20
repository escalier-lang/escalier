export const container = {value: 5, getValue(temp1) {
  const defaultValue = temp1;
  if (this.value != 0) {
    return this.value;
  } else {
    return defaultValue;
  }
}, addNums1(temp2, temp3) {
  const a = temp2;
  const b = temp3;
  return a + b;
}, addStrs1(temp4, temp5) {
  const a = temp4;
  const b = temp5;
  return this.addNums1(a, b);
}, addNums2(temp6, temp7) {
  const a = temp6;
  const b = temp7;
  return this.addNums1(a, b);
}};
export const a = container.getValue("default");
export const b = container.getValue(10);
export const c = container.addNums2("hello", 5);
//# sourceMappingURL=./index.js.map
