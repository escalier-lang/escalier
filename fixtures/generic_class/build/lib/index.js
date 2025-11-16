export class Box {
  constructor(temp3) {
    const value = temp3;
    this.value = value;
  }
  getValue(temp1) {
    const defaultValue = temp1;
    let temp2;
    if (this.value != 0) {
      return this.value;
    } else {
      return defaultValue;
    }
    temp2;
  }
}
export const box = new Box(5);
export const a = box.getValue("default");
export const b = box.getValue(10);
//# sourceMappingURL=./index.js.map
