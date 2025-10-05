class Box {
  constructor(temp3) {
    const value = temp3;
    this.value = value;
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
}
const box = Box(5);
const a = box.getValue("default");
const b = box.getValue(10);
//# sourceMappingURL=./index.js.map
