export class Box {
  constructor(temp2) {
    const value = temp2;
    this.value = value;
  }
  getValue(temp1) {
    const defaultValue = temp1;
    if (this.value != 0) {
      return this.value;
    } else {
      return defaultValue;
    }
  }
}
export const box = new Box(5);
export const a = box.getValue("default");
export const b = box.getValue(10);
//# sourceMappingURL=./index.js.map
